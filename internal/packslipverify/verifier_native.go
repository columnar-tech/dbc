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
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

const (
	// maxBundleSize bounds the bundle JSON accepted before parsing.
	maxBundleSize = 1 << 20

	bundleMediaTypeV03 = "application/vnd.dev.sigstore.bundle.v0.3+json"
	payloadTypeInToto  = "application/vnd.in-toto+json"
)

// ErrKeySigned reports a bundle signed with a long-lived key rather than a
// Fulcio certificate. It wraps [ErrUnsupported].
var ErrKeySigned = fmt.Errorf("%w: key-signed bundles (sigstore-key) are not supported yet", ErrUnsupported)

type nativeVerifier struct {
	// staticRoot is the immutable root parsed from Config.TrustedRootJSON, or
	// nil when the public-good TUF repository is used.
	staticRoot *root.TrustedRoot
	httpClient *http.Client
	cacheDir   string
	// loadRoot loads the trusted root through TUF; tests replace it.
	loadRoot rootLoader
}

// New returns a Verifier backed by sigstore-go.
func New(cfg Config) (Verifier, error) {
	v := &nativeVerifier{loadRoot: root.FetchTrustedRootWithOptions}
	if len(cfg.TrustedRootJSON) > 0 {
		tr, err := root.NewTrustedRootFromJSON(cfg.TrustedRootJSON)
		if err != nil {
			return nil, fmt.Errorf("parse trusted root: %w", err)
		}
		v.staticRoot = tr
		return v, nil
	}
	if err := v.configureTUF(cfg); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *nativeVerifier) VerifyKeyless(ctx context.Context, bundleJSON []byte, expectedIssuer string) (*Verified, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if expectedIssuer == "" {
		return nil, errors.New("packslip: expected OIDC issuer is required")
	}
	if len(bundleJSON) > maxBundleSize {
		return nil, fmt.Errorf("packslip: bundle is larger than %d bytes", maxBundleSize)
	}

	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return nil, fmt.Errorf("packslip: parse Sigstore bundle: %w", err)
	}
	if b.MediaType != bundleMediaTypeV03 {
		return nil, fmt.Errorf("packslip: unsupported bundle media type %q, want %q", b.MediaType, bundleMediaTypeV03)
	}
	envelope := b.GetDsseEnvelope()
	if envelope == nil {
		return nil, errors.New("packslip: bundle does not contain a DSSE envelope")
	}
	if envelope.GetPayloadType() != payloadTypeInToto {
		return nil, fmt.Errorf("packslip: DSSE payload type %q, want %q", envelope.GetPayloadType(), payloadTypeInToto)
	}
	material := b.GetVerificationMaterial()
	if material.GetPublicKey() != nil {
		return nil, ErrKeySigned
	}
	if material.GetCertificate() == nil {
		return nil, errors.New("packslip: bundle has no signing certificate")
	}

	tr, err := v.trustedRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("packslip: load Sigstore trusted root: %w", err)
	}

	// Options follow sigstore-go's public-good verification example. Signed
	// timestamps are intentionally not required, so bundles that only carry
	// the Rekor integrated time are accepted.
	sv, err := verify.NewVerifier(tr,
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
		verify.WithSignedCertificateTimestamps(1),
	)
	if err != nil {
		return nil, fmt.Errorf("packslip: create Sigstore verifier: %w", err)
	}

	// Pin the OIDC issuer exactly and accept any subject alternative name; the
	// repository and workflow decision belongs to the caller, which reads it
	// from the returned Claims.
	identity, err := verify.NewShortCertificateIdentity(expectedIssuer, "", "", ".*")
	if err != nil {
		return nil, fmt.Errorf("packslip: build certificate identity policy: %w", err)
	}
	// WithoutArtifactUnsafe: the bundle signs an in-toto statement, not one
	// artifact, so there is no artifact to check here. This is safe because
	// callers check artifact digests against the statement in the payload,
	// which is only trusted after this verification succeeded.
	result, err := sv.Verify(&b, verify.NewPolicy(
		verify.WithoutArtifactUnsafe(),
		verify.WithCertificateIdentity(identity),
	))
	if err != nil {
		return nil, fmt.Errorf("packslip: verify Sigstore bundle: %w", err)
	}
	if result == nil || result.Signature == nil || result.Signature.Certificate == nil {
		return nil, errors.New("packslip: verification returned no signing certificate")
	}

	cert := result.Signature.Certificate
	if cert.Issuer != expectedIssuer {
		return nil, fmt.Errorf("packslip: certificate OIDC issuer %q, want %q", cert.Issuer, expectedIssuer)
	}
	return &Verified{
		// Payload comes from the parsed envelope that was verified; copy it so
		// the result does not alias the bundle.
		Payload: bytes.Clone(envelope.GetPayload()),
		Certificate: Claims{
			Issuer:                     cert.Issuer,
			SubjectAlternativeName:     cert.SubjectAlternativeName,
			SourceRepositoryURI:        cert.SourceRepositoryURI,
			SourceRepositoryIdentifier: cert.SourceRepositoryIdentifier,
			SourceRepositoryRef:        cert.SourceRepositoryRef,
			BuildSignerURI:             cert.BuildSignerURI,
			RunInvocationURI:           cert.RunInvocationURI,
		},
	}, nil
}
