// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !js

package packslipverify

import (
	"context"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/columnar-tech/dbc/internal"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	sigtuf "github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/util"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/theupdateframework/go-tuf/v2/metadata/fetcher"
)

type nativeVerifier struct {
	config                Config
	configuredTrustedRoot *root.TrustedRoot
}

// New returns a native Sigstore bundle verifier.
func New(config Config) (PackslipVerifier, error) {
	verifier := &nativeVerifier{config: config}
	if config.TrustedRootJSON != nil {
		trustedRoot, err := root.NewTrustedRootFromJSON(config.TrustedRootJSON)
		if err != nil {
			return nil, fmt.Errorf("parse configured Sigstore trusted root: %w", err)
		}
		verifier.configuredTrustedRoot = trustedRoot
		verifier.config.TrustedRootJSON = nil
		return verifier, nil
	}

	if !config.DisableLocalCache {
		if config.CachePath == "" {
			configPath, err := internal.GetUserConfigPath()
			if err != nil {
				return nil, fmt.Errorf("find dbc user config directory for Sigstore cache: %w", err)
			}
			// Keep TUF state separate from other Sigstore tools so the lock and
			// cache format have one owner and one synchronization policy.
			config.CachePath = filepath.Join(configPath, "cache", "sigstore")
		}
		if !filepath.IsAbs(config.CachePath) {
			return nil, fmt.Errorf("Sigstore TUF cache path must be absolute: %q", config.CachePath)
		}
	}
	verifier.config = config
	return verifier, nil
}

func (v *nativeVerifier) Verify(ctx context.Context, bundleJSON []byte, identity IdentityPolicy, digest ArtifactDigest) (*VerifiedBundle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(bundleJSON) == 0 {
		return nil, errors.New("Sigstore bundle is empty")
	}
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	digestBytes, err := decodeDigest(digest)
	if err != nil {
		return nil, err
	}

	var signedBundle bundle.Bundle
	if err := signedBundle.UnmarshalJSON(bundleJSON); err != nil {
		return nil, fmt.Errorf("decode Sigstore bundle: %w", err)
	}

	trustedRoot, err := v.trustedRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("load Sigstore trusted root: %w", err)
	}

	certificateIdentity, err := verify.NewShortCertificateIdentity(identity.Issuer, "", identity.Subject, identity.SubjectRegex)
	if err != nil {
		return nil, fmt.Errorf("invalid signing identity policy: %w", err)
	}

	sigstoreVerifier, err := verify.NewVerifier(
		trustedRoot,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithObserverTimestamps(1),
		verify.WithTransparencyLog(1),
	)
	if err != nil {
		return nil, fmt.Errorf("create Sigstore verifier: %w", err)
	}

	result, err := sigstoreVerifier.Verify(
		&signedBundle,
		verify.NewPolicy(
			verify.WithArtifactDigest(digest.Algorithm, digestBytes),
			verify.WithCertificateIdentity(certificateIdentity),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("verify Sigstore bundle: %w", err)
	}
	if result == nil || result.Signature == nil || result.Signature.Certificate == nil {
		return nil, errors.New("Sigstore bundle did not verify with a signing certificate")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	verified := &VerifiedBundle{
		Issuer:        result.Signature.Certificate.Issuer,
		Signer:        result.Signature.Certificate.SubjectAlternativeName,
		SignedPayload: signedBundle.GetDsseEnvelope().GetPayload(),
	}
	if result.Statement != nil {
		statement := &Statement{
			Type:          result.Statement.GetType(),
			PredicateType: result.Statement.GetPredicateType(),
		}
		for _, subject := range result.Statement.GetSubject() {
			statement.Subjects = append(statement.Subjects, Subject{
				Name:    subject.GetName(),
				Digests: subject.GetDigest(),
			})
		}
		verified.Statement = statement
	}
	return verified, nil
}

// trustedRoot returns a configured immutable root or performs a fresh,
// caller-scoped TUF load. It retains no process-local TUF result.
func (v *nativeVerifier) trustedRoot(ctx context.Context) (*root.TrustedRoot, error) {
	return v.trustedRootWithLoader(ctx, root.FetchTrustedRootWithOptions)
}

// trustedRootWithLoader keeps the loader as a call argument so tests can
// exercise the real per-call cache/lock path without adding shared verifier
// state. Production always passes root.FetchTrustedRootWithOptions.
func (v *nativeVerifier) trustedRootWithLoader(
	ctx context.Context,
	load func(*sigtuf.Options) (*root.TrustedRoot, error),
) (*root.TrustedRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if v.configuredTrustedRoot != nil {
		return v.configuredTrustedRoot, nil
	}

	opts := sigtuf.DefaultOptions()
	opts.Context = ctx
	opts.DisableLocalCache = v.config.DisableLocalCache
	if !opts.DisableLocalCache {
		opts.CachePath = v.config.CachePath
	}

	client := v.config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	metadataFetcher := fetcher.NewDefaultFetcher()
	metadataFetcher.SetHTTPUserAgent(util.ConstructUserAgent())
	metadataFetcher.SetHTTPClient(requestContextHTTPClient{ctx: ctx, client: client})
	opts.Fetcher = metadataFetcher

	return loadTrustedRootWithCache(ctx, opts, load)
}

// loadTrustedRootWithCache serializes all TUF filesystem access for a cache
// path, including client construction and target retrieval. Each call uses a
// fresh TUF client; no process-local trusted-root state survives the call.
func loadTrustedRootWithCache(
	ctx context.Context,
	opts *sigtuf.Options,
	load func(*sigtuf.Options) (*root.TrustedRoot, error),
) (*root.TrustedRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if load == nil {
		return nil, errors.New("Sigstore trusted root loader is nil")
	}
	opts.Context = ctx
	if opts.DisableLocalCache {
		return fetchTrustedRoot(ctx, opts, load)
	}
	if !filepath.IsAbs(opts.CachePath) {
		return nil, fmt.Errorf("Sigstore TUF cache path must be absolute: %q", opts.CachePath)
	}
	if err := os.MkdirAll(opts.CachePath, 0o700); err != nil {
		return nil, fmt.Errorf("create Sigstore TUF cache directory: %w", err)
	}
	lock, err := fslock.AcquireContext(ctx, filepath.Join(opts.CachePath, "tuf.lock"))
	if err != nil {
		return nil, fmt.Errorf("acquire Sigstore TUF cache lock: %w", err)
	}

	trustedRoot, fetchErr := fetchTrustedRoot(ctx, opts, load)
	releaseErr := lock.Release()
	if fetchErr != nil {
		if releaseErr != nil {
			return nil, errors.Join(fetchErr, fmt.Errorf("release Sigstore TUF cache lock: %w", releaseErr))
		}
		return nil, fetchErr
	}
	if releaseErr != nil {
		return nil, fmt.Errorf("release Sigstore TUF cache lock: %w", releaseErr)
	}
	return trustedRoot, nil
}

func fetchTrustedRoot(
	ctx context.Context,
	opts *sigtuf.Options,
	load func(*sigtuf.Options) (*root.TrustedRoot, error),
) (*root.TrustedRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	trustedRoot, err := load(opts)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	if trustedRoot == nil {
		return nil, errors.New("Sigstore trusted root loader returned no root")
	}
	return trustedRoot, nil
}

// requestContextHTTPClient adapts go-tuf's context-free Fetcher interface to
// the caller's context without modifying the shared HTTP client or transport.
type requestContextHTTPClient struct {
	ctx    context.Context
	client interface {
		Do(*http.Request) (*http.Response, error)
	}
}

func (c requestContextHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	return c.client.Do(req.Clone(c.ctx))
}

func validateIdentity(identity IdentityPolicy) error {
	if strings.TrimSpace(identity.Issuer) == "" {
		return errors.New("signing identity issuer is required")
	}
	if (identity.Subject == "") == (identity.SubjectRegex == "") {
		return errors.New("specify exactly one of signing identity subject or subject regex")
	}
	if identity.SubjectRegex != "" {
		if !strings.HasPrefix(identity.SubjectRegex, "^") || !strings.HasSuffix(identity.SubjectRegex, "$") {
			return errors.New("signing identity subject regex must be anchored with ^ and $")
		}
		if _, err := regexp.Compile(identity.SubjectRegex); err != nil {
			return fmt.Errorf("invalid signing identity subject regex: %w", err)
		}
	}
	return nil
}

func decodeDigest(digest ArtifactDigest) ([]byte, error) {
	var hash crypto.Hash
	switch digest.Algorithm {
	case "sha256":
		hash = crypto.SHA256
	case "sha512":
		hash = crypto.SHA512
	default:
		return nil, fmt.Errorf("unsupported artifact digest algorithm %q", digest.Algorithm)
	}
	decoded, err := hex.DecodeString(digest.Hex)
	if err != nil {
		return nil, fmt.Errorf("invalid %s artifact digest: %w", digest.Algorithm, err)
	}
	if len(decoded) != hash.Size() {
		return nil, fmt.Errorf("invalid %s artifact digest length: got %d bytes, want %d", digest.Algorithm, len(decoded), hash.Size())
	}
	return decoded, nil
}

var _ PackslipVerifier = (*nativeVerifier)(nil)
