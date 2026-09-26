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

// Package packslipverify provides the signature verification boundary used by
// Packslip release resolution. Platform-specific implementations are kept out
// of callers so unsupported targets fail explicitly.
package packslipverify

import (
	"context"
	"errors"
	"net/http"
)

var ErrUnsupported = errors.New("Sigstore bundle verification is unsupported on this target")

// Config controls trusted-root retrieval. HTTPClient is used for Sigstore TUF
// requests, and CachePath selects the persistent TUF metadata cache. When
// caching is enabled and CachePath is empty, dbc uses its own user config cache
// directory. A TrustedRootJSON value is parsed when New is called and avoids
// TUF network and cache access during verification.
type Config struct {
	HTTPClient        *http.Client
	CachePath         string
	DisableLocalCache bool
	TrustedRootJSON   []byte
}

// IdentityPolicy pins the signing certificate to an OIDC issuer and either an
// exact certificate SAN or an anchored regular expression for that SAN.
type IdentityPolicy struct {
	Issuer       string
	Subject      string
	SubjectRegex string
}

// ArtifactDigest identifies the subject that the verified signed statement
// must cover. Only SHA-256 and SHA-512 are accepted.
type ArtifactDigest struct {
	Algorithm string
	Hex       string
}

// Subject is an artifact named by a verified in-toto statement.
type Subject struct {
	Name    string
	Digests map[string]string
}

// Statement contains the verified in-toto statement fields needed by release
// resolution. SignedPayload retains the original payload for format-specific
// validation by the caller.
type Statement struct {
	Type          string
	PredicateType string
	Subjects      []Subject
}

// VerifiedBundle is returned only after Sigstore cryptographic verification,
// signing identity checks, and the artifact digest policy have passed.
type VerifiedBundle struct {
	Issuer        string
	Signer        string
	SignedPayload []byte
	Statement     *Statement
}

// PackslipVerifier verifies a Sigstore bundle against an expected signer and
// artifact digest. Implementations must fail closed when verification is not
// available.
type PackslipVerifier interface {
	Verify(context.Context, []byte, IdentityPolicy, ArtifactDigest) (*VerifiedBundle, error)
}
