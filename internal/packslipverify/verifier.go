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

// Package packslipverify verifies Packslip release bundles: Sigstore bundles
// (v0.3) holding a DSSE envelope with an in-toto payload, signed keylessly
// (the "sigstore-oidc" scheme).
//
// The package answers one question: is this bundle cryptographically valid,
// and which signing certificate claims does it carry? It does not decide
// whether the signer is trusted. Callers must apply the Packslip consumer
// rules to the returned [Claims] and parse the returned payload themselves.
package packslipverify

import (
	"context"
	"errors"
	"net/http"
)

// ErrUnsupported reports that Sigstore bundle verification is not available,
// either on this platform (Wasm) or for this kind of bundle (key-signed).
var ErrUnsupported = errors.New("packslip: Sigstore bundle verification is unsupported")

// Config configures [New].
type Config struct {
	// HTTPClient is used for Sigstore TUF requests. It is never modified.
	// Nil selects a client with dial, TLS handshake, response header and
	// overall timeouts.
	HTTPClient *http.Client

	// CacheDir is the absolute directory that holds the TUF metadata cache and
	// its lock file. Empty selects a dedicated directory under dbc's user
	// config directory.
	CacheDir string

	// TrustedRootJSON, if set, is a Sigstore trusted root that is used
	// instead of the public-good TUF repository. It is parsed once by New;
	// verification then makes no network, cache or lock access.
	TrustedRootJSON []byte
}

// Verifier verifies keyless Packslip bundles.
type Verifier interface {
	// VerifyKeyless verifies the bundle's signature, certificate chain,
	// transparency log entry and signed certificate timestamp against the
	// Sigstore trusted root, requires the certificate's OIDC issuer (the
	// Fulcio issuer extension, not the X.509 issuer name) to equal
	// expectedIssuer, and requires a DSSE envelope with payload type
	// application/vnd.in-toto+json. Only then does it return a result. On any
	// error the result is nil; claims of an unverified bundle are never
	// returned.
	//
	// The signing identity is deliberately not constrained here: any
	// subject alternative name is accepted. That does not mean the publisher
	// is trusted. The result is a cryptographically verified payload plus the
	// verified certificate's claims. Project binding, repository identity,
	// workflow, the identity block, the driver marker and artifact validity
	// are NOT checked. Because the bundle signs a statement rather than a
	// single artifact, artifact digests are not checked at this layer either;
	// callers must check them against the verified statement.
	//
	// Cancelling ctx aborts trusted root retrieval. The CPU-bound signature
	// verification itself is not cancellable.
	VerifyKeyless(ctx context.Context, bundleJSON []byte, expectedIssuer string) (*Verified, error)
}

// Verified is the result of a successful verification.
type Verified struct {
	// Payload is the exact DSSE payload that was signed. It is never
	// re-serialized.
	Payload []byte
	// Certificate holds claims read from the verified signing certificate.
	Certificate Claims
}

// Claims are the signing certificate claims relevant to Packslip. Owner URI
// and owner ID are intentionally not exposed: the specification forbids
// remembering or comparing them.
type Claims struct {
	// Issuer is the OIDC issuer (Fulcio issuer extension).
	Issuer string
	// SubjectAlternativeName identifies the signer, for example a workflow URI
	// with its ref.
	SubjectAlternativeName string
	// SourceRepositoryURI may be empty for old certificates.
	SourceRepositoryURI string
	// SourceRepositoryIdentifier is the forge's repository ID; it may be empty.
	SourceRepositoryIdentifier string
	SourceRepositoryRef        string
	BuildSignerURI             string
	RunInvocationURI           string
}
