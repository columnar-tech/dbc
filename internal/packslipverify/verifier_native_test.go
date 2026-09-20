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

// The signed bundle and trusted root fixtures come from
// github.com/sigstore/sigstore-go v1.3.0 examples.

//go:build !js

package packslipverify

import (
	"context"
	"embed"
	"strings"
	"testing"
)

//go:embed testdata/*.json
var bundleFixture embed.FS

func newFixtureVerifier(t *testing.T) PackslipVerifier {
	t.Helper()
	trustedRoot, err := bundleFixture.ReadFile("testdata/trusted-root-public-good.json")
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := New(Config{TrustedRootJSON: trustedRoot})
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

const (
	fixtureIssuer  = "https://token.actions.githubusercontent.com"
	fixtureSubject = `^https://github\.com/sigstore/sigstore-js/\.github/workflows/release\.yml@refs/heads/main$`
	fixtureDigest  = "76176ffa33808b54602c7c35de5c6e9a4deb96066dba6533f50ac234f4f1f4c6b3527515dc17c06fbe2860030f410eee69ea20079bd3a2c6f3dcf3b329b10751"
)

func TestNativeVerifierVerifiesSignedBundle(t *testing.T) {
	fixture, err := bundleFixture.ReadFile("testdata/bundle-provenance.json")
	if err != nil {
		t.Fatal(err)
	}

	verifier := newFixtureVerifier(t)
	result, err := verifier.Verify(context.Background(), fixture, IdentityPolicy{
		Issuer:       fixtureIssuer,
		SubjectRegex: fixtureSubject,
	}, ArtifactDigest{Algorithm: "sha512", Hex: fixtureDigest})
	if err != nil {
		t.Fatalf("verify signed fixture: %v", err)
	}
	if result.Issuer != fixtureIssuer {
		t.Fatalf("verified issuer = %q, want %q", result.Issuer, fixtureIssuer)
	}
	wantSigner := "https://github.com/sigstore/sigstore-js/.github/workflows/release.yml@refs/heads/main"
	if result.Signer != wantSigner {
		t.Fatalf("verified signer = %q, want %q", result.Signer, wantSigner)
	}
	if result.Statement == nil || len(result.Statement.Subjects) != 1 {
		t.Fatalf("verified statement = %#v, want one subject", result.Statement)
	}
	if got := result.Statement.Subjects[0].Digests["sha512"]; got != fixtureDigest {
		t.Fatalf("verified subject digest = %q, want %q", got, fixtureDigest)
	}
	if !strings.Contains(string(result.SignedPayload), "https://in-toto.io/Statement/v0.1") {
		t.Fatalf("verified payload does not contain the in-toto statement: %s", result.SignedPayload)
	}
}

func TestNativeVerifierRejectsWrongDigestAndIdentity(t *testing.T) {
	fixture, err := bundleFixture.ReadFile("testdata/bundle-provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	verifier := newFixtureVerifier(t)
	identity := IdentityPolicy{Issuer: fixtureIssuer, SubjectRegex: fixtureSubject}

	if result, err := verifier.Verify(context.Background(), fixture, identity, ArtifactDigest{
		Algorithm: "sha512",
		Hex:       strings.Repeat("0", 128),
	}); err == nil || result != nil {
		t.Fatalf("wrong artifact digest returned result %#v, error %v", result, err)
	}

	if result, err := verifier.Verify(context.Background(), fixture, IdentityPolicy{
		Issuer:       fixtureIssuer,
		SubjectRegex: `^https://github\.com/attacker/.*$`,
	}, ArtifactDigest{Algorithm: "sha512", Hex: fixtureDigest}); err == nil || result != nil {
		t.Fatalf("wrong signing identity returned result %#v, error %v", result, err)
	}
}

func TestNativeVerifierRejectsUnanchoredIdentityRegex(t *testing.T) {
	verifier := newFixtureVerifier(t)
	_, err := verifier.Verify(context.Background(), []byte("{}"), IdentityPolicy{
		Issuer:       fixtureIssuer,
		SubjectRegex: `sigstore-js`,
	}, ArtifactDigest{Algorithm: "sha512", Hex: fixtureDigest})
	if err == nil || !strings.Contains(err.Error(), "must be anchored") {
		t.Fatalf("error = %v, want unanchored identity policy error", err)
	}
}
