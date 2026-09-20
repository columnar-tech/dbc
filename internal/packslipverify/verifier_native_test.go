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
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
)

//go:embed testdata/*.json
var bundleFixture embed.FS

func readFixtureTrustedRoot(t *testing.T) *root.TrustedRoot {
	t.Helper()
	trustedRootJSON, err := bundleFixture.ReadFile("testdata/trusted-root-public-good.json")
	if err != nil {
		t.Fatal(err)
	}
	trustedRoot, err := root.NewTrustedRootFromJSON(trustedRootJSON)
	if err != nil {
		t.Fatal(err)
	}
	return trustedRoot
}

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

func TestTrustedRootWaitHonorsCallerCancellationAndReusesSuccess(t *testing.T) {
	trustedRoot := readFixtureTrustedRoot(t)
	loaderStarted := make(chan struct{})
	allowLoadToFinish := make(chan struct{})
	waiterStarted := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(allowLoadToFinish) })
	var calls atomic.Int32

	verifier := &nativeVerifier{
		rootLoader: func(ctx context.Context) (*root.TrustedRoot, error) {
			calls.Add(1)
			close(loaderStarted)
			select {
			case <-allowLoadToFinish:
				return trustedRoot, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		rootWaitHook: func() { close(waiterStarted) },
	}
	type rootResult struct {
		root *root.TrustedRoot
		err  error
	}
	firstResult := make(chan rootResult, 1)
	go func() {
		got, err := verifier.trustedRoot(context.Background())
		firstResult <- rootResult{root: got, err: err}
	}()
	select {
	case <-loaderStarted:
	case <-time.After(time.Second):
		t.Fatal("root loader did not start")
	}

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	defer cancelWaiter()
	waiterResult := make(chan rootResult, 1)
	go func() {
		got, err := verifier.trustedRoot(waiterCtx)
		waiterResult <- rootResult{root: got, err: err}
	}()
	select {
	case <-waiterStarted:
	case <-time.After(time.Second):
		t.Fatal("second caller did not join the in-flight root load")
	}

	cancelWaiter()
	select {
	case result := <-waiterResult:
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("second caller error = %v, want context cancellation", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("second caller did not return promptly after cancellation")
	}

	releaseOnce.Do(func() { close(allowLoadToFinish) })
	first := <-firstResult
	if first.err != nil || first.root != trustedRoot {
		t.Fatalf("first caller got root %p, error %v; want shared root %p", first.root, first.err, trustedRoot)
	}
	reused, err := verifier.trustedRoot(context.Background())
	if err != nil || reused != trustedRoot {
		t.Fatalf("cached root = %p, error %v; want shared root %p", reused, err, trustedRoot)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("root loader calls = %d, want 1", got)
	}
}

func TestTrustedRootFailureIsSharedThenRetried(t *testing.T) {
	trustedRoot := readFixtureTrustedRoot(t)
	temporaryFailure := errors.New("temporary trust root fetch failure")
	loaderStarted := make(chan struct{})
	allowFailure := make(chan struct{})
	waiterStarted := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(allowFailure) })
	var calls atomic.Int32

	verifier := &nativeVerifier{
		rootLoader: func(context.Context) (*root.TrustedRoot, error) {
			if calls.Add(1) == 1 {
				close(loaderStarted)
				<-allowFailure
				return nil, temporaryFailure
			}
			return trustedRoot, nil
		},
		rootWaitHook: func() { close(waiterStarted) },
	}
	type rootResult struct {
		root *root.TrustedRoot
		err  error
	}
	firstResult := make(chan rootResult, 1)
	go func() {
		got, err := verifier.trustedRoot(context.Background())
		firstResult <- rootResult{root: got, err: err}
	}()
	select {
	case <-loaderStarted:
	case <-time.After(time.Second):
		t.Fatal("root loader did not start")
	}
	waiterResult := make(chan rootResult, 1)
	go func() {
		got, err := verifier.trustedRoot(context.Background())
		waiterResult <- rootResult{root: got, err: err}
	}()
	select {
	case <-waiterStarted:
	case <-time.After(time.Second):
		t.Fatal("second caller did not join the in-flight root load")
	}

	releaseOnce.Do(func() { close(allowFailure) })
	for name, resultChannel := range map[string]<-chan rootResult{
		"initializer": firstResult,
		"waiter":      waiterResult,
	} {
		select {
		case result := <-resultChannel:
			if result.root != nil || !errors.Is(result.err, temporaryFailure) {
				t.Errorf("%s got root %p, error %v; want the shared initialization error", name, result.root, result.err)
			}
		case <-time.After(time.Second):
			t.Errorf("%s did not receive the initialization failure", name)
		}
	}

	retried, err := verifier.trustedRoot(context.Background())
	if err != nil || retried != trustedRoot {
		t.Fatalf("retry got root %p, error %v; want root %p", retried, err, trustedRoot)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("root loader calls = %d, want 2 after one retry", got)
	}
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
