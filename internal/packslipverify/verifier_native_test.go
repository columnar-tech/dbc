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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	githubIssuer  = "https://token.actions.githubusercontent.com"
	fixtureBundle = "testdata/hk-v2.12.0-packslip.sigstore.json"
	fixtureRoot   = "testdata/trusted-root-public-good.json"
)

func readFile(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// jsonMap decodes JSON into a generic map so tests can mutate a fixture.
func jsonMap(t testing.TB, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func marshal(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func staticVerifier(t testing.TB, rootJSON []byte) Verifier {
	t.Helper()
	v, err := New(Config{TrustedRootJSON: rootJSON})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mutateBundle(t testing.TB, mutate func(m map[string]any)) []byte {
	t.Helper()
	m := jsonMap(t, readFile(t, fixtureBundle))
	mutate(m)
	return marshal(t, m)
}

func assertRejected(t *testing.T, v Verifier, bundle []byte, issuer string) error {
	t.Helper()
	got, err := v.VerifyKeyless(context.Background(), bundle, issuer)
	if err == nil {
		t.Fatalf("VerifyKeyless succeeded with %+v, want error", got)
	}
	t.Logf("rejected: %v", err)
	if got != nil {
		t.Fatalf("result = %+v on error, want nil", got)
	}
	return err
}

// Success also documents that policy is not enforced here: the caller passes
// only the issuer, so a bundle signed by any repository under that issuer
// verifies, and deciding whether the repository or workflow is the expected
// one is left to the caller reading the returned Claims.
func TestVerifyKeylessSuccess(t *testing.T) {
	bundle := readFile(t, fixtureBundle)
	v := staticVerifier(t, readFile(t, fixtureRoot))

	got, err := v.VerifyKeyless(context.Background(), bundle, githubIssuer)
	if err != nil {
		t.Fatalf("VerifyKeyless: %v", err)
	}

	// The payload must be the exact signed bytes, not a re-serialization.
	var raw struct {
		DSSE struct {
			Payload string `json:"payload"`
		} `json:"dsseEnvelope"`
	}
	if err := json.Unmarshal(bundle, &raw); err != nil {
		t.Fatal(err)
	}
	want, err := base64.StdEncoding.DecodeString(raw.DSSE.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Payload, want) {
		t.Errorf("payload differs from the DSSE payload bytes")
	}

	c := got.Certificate
	if c.Issuer != githubIssuer {
		t.Errorf("Issuer = %q", c.Issuer)
	}
	if !strings.HasPrefix(c.SubjectAlternativeName, "https://github.com/jdx/") {
		t.Errorf("SubjectAlternativeName = %q", c.SubjectAlternativeName)
	}
	// The hk release is signed by a run of github.com/jdx/hk.
	if c.SourceRepositoryURI != "https://github.com/jdx/hk" {
		t.Errorf("SourceRepositoryURI = %q", c.SourceRepositoryURI)
	}
	if c.SourceRepositoryIdentifier != "922514152" { // GitHub repository ID of jdx/hk
		t.Errorf("SourceRepositoryIdentifier is empty")
	}
	if !strings.HasPrefix(c.SourceRepositoryRef, "refs/") {
		t.Errorf("SourceRepositoryRef = %q", c.SourceRepositoryRef)
	}
	if c.BuildSignerURI == "" || !strings.HasPrefix(c.RunInvocationURI, "https://github.com/jdx/hk/actions/runs/") {
		t.Errorf("BuildSignerURI = %q, RunInvocationURI = %q", c.BuildSignerURI, c.RunInvocationURI)
	}
	t.Logf("claims: %+v", c)

	// Mutating the result must not affect a later verification of the same
	// bundle bytes.
	got.Payload[0] ^= 0xff
	again, err := v.VerifyKeyless(context.Background(), bundle, githubIssuer)
	if err != nil || !bytes.Equal(again.Payload, want) {
		t.Fatalf("second verification: %v", err)
	}
}

func TestVerifyKeylessRejectsIssuer(t *testing.T) {
	v := staticVerifier(t, readFile(t, fixtureRoot))
	bundle := readFile(t, fixtureBundle)
	assertRejected(t, v, bundle, "https://accounts.example.com")
	assertRejected(t, v, bundle, githubIssuer+"/")
	if err := assertRejected(t, v, bundle, ""); err == nil {
		t.Fatal("empty issuer accepted")
	}
}

func TestVerifyKeylessTamperedPayload(t *testing.T) {
	v := staticVerifier(t, readFile(t, fixtureRoot))
	bundle := mutateBundle(t, func(m map[string]any) {
		env := m["dsseEnvelope"].(map[string]any)
		payload, err := base64.StdEncoding.DecodeString(env["payload"].(string))
		if err != nil {
			t.Fatal(err)
		}
		payload = append(payload, ' ') // trailing whitespace keeps the JSON valid
		env["payload"] = base64.StdEncoding.EncodeToString(payload)
	})
	assertRejected(t, v, bundle, githubIssuer)
}

func TestVerifyKeylessTamperedSignature(t *testing.T) {
	v := staticVerifier(t, readFile(t, fixtureRoot))
	bundle := mutateBundle(t, func(m map[string]any) {
		sigs := m["dsseEnvelope"].(map[string]any)["signatures"].([]any)
		sig := sigs[0].(map[string]any)
		raw, err := base64.StdEncoding.DecodeString(sig["sig"].(string))
		if err != nil {
			t.Fatal(err)
		}
		raw[len(raw)-1] ^= 0x01
		sig["sig"] = base64.StdEncoding.EncodeToString(raw)
	})
	assertRejected(t, v, bundle, githubIssuer)
}

func TestVerifyKeylessPayloadTypeChangedOnly(t *testing.T) {
	v := staticVerifier(t, readFile(t, fixtureRoot))
	bundle := mutateBundle(t, func(m map[string]any) {
		m["dsseEnvelope"].(map[string]any)["payloadType"] = "application/json"
	})
	err := assertRejected(t, v, bundle, githubIssuer)
	if !strings.Contains(err.Error(), "payload type") {
		t.Errorf("err = %v, want payload type error", err)
	}
}

func TestVerifyKeylessNotDSSE(t *testing.T) {
	v := staticVerifier(t, readFile(t, fixtureRoot))
	bundle := mutateBundle(t, func(m map[string]any) {
		delete(m, "dsseEnvelope")
		m["messageSignature"] = map[string]any{
			"messageDigest": map[string]any{
				"algorithm": "SHA2_256",
				"digest":    base64.StdEncoding.EncodeToString(make([]byte, 32)),
			},
			"signature": base64.StdEncoding.EncodeToString([]byte("sig")),
		}
	})
	err := assertRejected(t, v, bundle, githubIssuer)
	if !strings.Contains(err.Error(), "DSSE") {
		t.Errorf("err = %v, want DSSE error", err)
	}
}

func TestVerifyKeylessMediaType(t *testing.T) {
	v := staticVerifier(t, readFile(t, fixtureRoot))
	for _, mt := range []string{
		"application/vnd.dev.sigstore.bundle+json;version=0.1",
		"application/vnd.dev.sigstore.bundle+json;version=0.2",
		"application/vnd.dev.sigstore.bundle.v0.3.1+json",
		"application/vnd.dev.sigstore.bundle+json;version=0.3",
		"application/json",
	} {
		bundle := mutateBundle(t, func(m map[string]any) { m["mediaType"] = mt })
		assertRejected(t, v, bundle, githubIssuer)
	}
}

func TestVerifyKeylessMalformedAndOversized(t *testing.T) {
	v := staticVerifier(t, readFile(t, fixtureRoot))
	for _, in := range [][]byte{nil, []byte(""), []byte("{"), []byte("[]"), []byte("{}"), []byte("null")} {
		assertRejected(t, v, in, githubIssuer)
	}
	big := append(readFile(t, fixtureBundle), bytes.Repeat([]byte(" "), maxBundleSize)...)
	err := assertRejected(t, v, big, githubIssuer)
	if !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err = %v, want size error", err)
	}
}

func TestVerifyKeylessKeySignedBundle(t *testing.T) {
	v := staticVerifier(t, readFile(t, fixtureRoot))
	bundle := mutateBundle(t, func(m map[string]any) {
		vm := m["verificationMaterial"].(map[string]any)
		delete(vm, "certificate")
		vm["publicKey"] = map[string]any{"hint": "abcd"}
	})
	err := assertRejected(t, v, bundle, githubIssuer)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
	// Keyless failures must never be reported as unsupported.
	err = assertRejected(t, v, mutateBundle(t, func(m map[string]any) {
		sig := m["dsseEnvelope"].(map[string]any)["signatures"].([]any)[0].(map[string]any)
		sig["sig"] = base64.StdEncoding.EncodeToString([]byte("not a signature"))
	}), githubIssuer)
	if errors.Is(err, ErrUnsupported) {
		t.Errorf("keyless failure reported as unsupported: %v", err)
	}
}

func TestVerifyKeylessRequiresTransparencyLogEntry(t *testing.T) {
	v := staticVerifier(t, readFile(t, fixtureRoot))
	if _, err := v.VerifyKeyless(context.Background(), readFile(t, fixtureBundle), githubIssuer); err != nil {
		t.Fatalf("positive control: %v", err)
	}
	assertRejected(t, v, mutateBundle(t, func(m map[string]any) {
		m["verificationMaterial"].(map[string]any)["tlogEntries"] = []any{}
	}), githubIssuer)
	assertRejected(t, v, mutateBundle(t, func(m map[string]any) {
		delete(m["verificationMaterial"].(map[string]any), "tlogEntries")
	}), githubIssuer)
}

func TestVerifyKeylessInvalidTransparencyLogEntry(t *testing.T) {
	v := staticVerifier(t, readFile(t, fixtureRoot))
	assertRejected(t, v, mutateBundle(t, func(m map[string]any) {
		entry := m["verificationMaterial"].(map[string]any)["tlogEntries"].([]any)[0].(map[string]any)
		entry["canonicalizedBody"] = base64.StdEncoding.EncodeToString([]byte(`{"kind":"dsse"}`))
	}), githubIssuer)
	assertRejected(t, v, mutateBundle(t, func(m map[string]any) {
		entry := m["verificationMaterial"].(map[string]any)["tlogEntries"].([]any)[0].(map[string]any)
		proof := entry["inclusionProof"].(map[string]any)
		proof["logIndex"] = "1"
	}), githubIssuer)
}

func mutateRoot(t testing.TB, mutate func(m map[string]any)) []byte {
	t.Helper()
	m := jsonMap(t, readFile(t, fixtureRoot))
	mutate(m)
	return marshal(t, m)
}

// The positive control in TestVerifyKeylessSuccess shows these bundles verify
// against the full root; removing a log set from the root must make the
// corresponding verifier option fail.
func TestVerifyKeylessRequiresSCTAndTlogTrust(t *testing.T) {
	bundle := readFile(t, fixtureBundle)
	t.Run("no CT log keys", func(t *testing.T) {
		rootJSON := mutateRoot(t, func(m map[string]any) { m["ctlogs"] = []any{} })
		assertRejected(t, staticVerifier(t, rootJSON), bundle, githubIssuer)
	})
	t.Run("no transparency log keys", func(t *testing.T) {
		rootJSON := mutateRoot(t, func(m map[string]any) { m["tlogs"] = []any{} })
		assertRejected(t, staticVerifier(t, rootJSON), bundle, githubIssuer)
	})
}

func TestVerifyKeylessCertificateFromOtherRoot(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "other root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	rootJSON := mutateRoot(t, func(m map[string]any) {
		cas := m["certificateAuthorities"].([]any)
		for _, ca := range cas {
			chain := ca.(map[string]any)["certChain"].(map[string]any)
			chain["certificates"] = []any{map[string]any{"rawBytes": base64.StdEncoding.EncodeToString(der)}}
		}
	})
	assertRejected(t, staticVerifier(t, rootJSON), readFile(t, fixtureBundle), githubIssuer)
}

func TestNewRejectsBadTrustedRoot(t *testing.T) {
	if _, err := New(Config{TrustedRootJSON: []byte("{")}); err == nil {
		t.Fatal("New accepted invalid trusted root")
	}
}

func TestVerifyKeylessCancelledContext(t *testing.T) {
	v := staticVerifier(t, readFile(t, fixtureRoot))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := v.VerifyKeyless(ctx, readFile(t, fixtureBundle), githubIssuer)
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("got %v, %v; want nil, context.Canceled", got, err)
	}
}
