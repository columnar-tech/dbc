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
	"regexp"
	"strings"
	"sync"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	sigtuf "github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/theupdateframework/go-tuf/v2/metadata/fetcher"
)

type nativeVerifier struct {
	config Config

	rootMu       sync.Mutex
	root         *root.TrustedRoot
	rootLoad     *trustedRootLoad
	rootLoader   trustedRootLoader // Optional loader seam for native tests.
	rootWaitHook func()            // Optional synchronization hook for native tests.
}

type trustedRootLoad struct {
	done chan struct{}
	root *root.TrustedRoot
	err  error
}

type trustedRootLoader func(context.Context) (*root.TrustedRoot, error)

// New returns a native Sigstore bundle verifier.
func New(config Config) (PackslipVerifier, error) {
	config.TrustedRootJSON = append([]byte(nil), config.TrustedRootJSON...)
	return &nativeVerifier{config: config}, nil
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

// trustedRoot shares each in-flight load with its current waiters. A successful
// result is cached; a failed attempt is shared with its waiters and then
// discarded so a later caller can retry.
func (v *nativeVerifier) trustedRoot(ctx context.Context) (*root.TrustedRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	v.rootMu.Lock()
	if err := ctx.Err(); err != nil {
		v.rootMu.Unlock()
		return nil, err
	}
	if v.root != nil {
		trustedRoot := v.root
		v.rootMu.Unlock()
		return trustedRoot, nil
	}
	if currentLoad := v.rootLoad; currentLoad != nil {
		v.rootMu.Unlock()
		if v.rootWaitHook != nil {
			v.rootWaitHook()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-currentLoad.done:
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return currentLoad.root, currentLoad.err
		}
	}

	currentLoad := &trustedRootLoad{done: make(chan struct{})}
	v.rootLoad = currentLoad
	loader := v.rootLoader
	v.rootMu.Unlock()

	if loader == nil {
		loader = v.loadTrustedRoot
	}
	trustedRoot, err := loader(ctx)
	if err == nil {
		switch {
		case trustedRoot == nil:
			err = errors.New("trusted root loader returned no root")
		case ctx.Err() != nil:
			trustedRoot = nil
			err = ctx.Err()
		}
	}

	v.rootMu.Lock()
	currentLoad.root = trustedRoot
	currentLoad.err = err
	if err == nil {
		v.root = trustedRoot
	}
	if v.rootLoad == currentLoad {
		// Failed attempts are shared with their current waiters, then forgotten
		// so a later Verify call can retry transient TUF/network failures.
		v.rootLoad = nil
	}
	close(currentLoad.done)
	v.rootMu.Unlock()
	return trustedRoot, err
}

func (v *nativeVerifier) loadTrustedRoot(ctx context.Context) (*root.TrustedRoot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var trustedRoot *root.TrustedRoot
	var err error
	if len(v.config.TrustedRootJSON) > 0 {
		trustedRoot, err = root.NewTrustedRootFromJSON(v.config.TrustedRootJSON)
	} else {
		opts := sigtuf.DefaultOptions()
		if v.config.CachePath != "" {
			opts.CachePath = v.config.CachePath
		}
		opts.DisableLocalCache = v.config.DisableLocalCache
		opts.Context = ctx
		if v.config.HTTPClient != nil {
			metadataFetcher := fetcher.NewDefaultFetcher()
			metadataFetcher.SetHTTPClient(v.config.HTTPClient)
			opts.Fetcher = metadataFetcher
		}
		trustedRoot, err = root.FetchTrustedRootWithOptions(opts)
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return trustedRoot, nil
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
