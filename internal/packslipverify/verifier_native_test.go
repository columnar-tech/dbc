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
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/columnar-tech/dbc/internal"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/sigstore/sigstore-go/pkg/root"
	sigtuf "github.com/sigstore/sigstore-go/pkg/tuf"
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

func readFixtureTrustedRootJSON(t *testing.T) []byte {
	t.Helper()
	trustedRootJSON, err := bundleFixture.ReadFile("testdata/trusted-root-public-good.json")
	if err != nil {
		t.Fatal(err)
	}
	return trustedRootJSON
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

func newCacheVerifier(t *testing.T, cachePath string, disableCache bool) *nativeVerifier {
	t.Helper()
	verifier, err := New(Config{CachePath: cachePath, DisableLocalCache: disableCache})
	if err != nil {
		t.Fatal(err)
	}
	native, ok := verifier.(*nativeVerifier)
	if !ok {
		t.Fatalf("New returned %T, want *nativeVerifier", verifier)
	}
	return native
}

func TestNewParsesConfiguredTrustedRootAndParallelVerifyNeedsNoNetwork(t *testing.T) {
	if _, err := New(Config{TrustedRootJSON: []byte("{")}); err == nil {
		t.Fatal("New accepted invalid trusted-root JSON")
	}

	fixture, err := bundleFixture.ReadFile("testdata/bundle-provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var networkCalls atomic.Int32
	httpClient := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		networkCalls.Add(1)
		return nil, errors.New("unexpected TUF network request")
	})}
	verifier, err := New(Config{
		TrustedRootJSON: readFixtureTrustedRootJSON(t),
		HTTPClient:      httpClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := IdentityPolicy{Issuer: fixtureIssuer, SubjectRegex: fixtureSubject}
	digest := ArtifactDigest{Algorithm: "sha512", Hex: fixtureDigest}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := verifier.Verify(context.Background(), fixture, identity, digest)
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Errorf("parallel Verify: %v", err)
		}
	}
	if got := networkCalls.Load(); got != 0 {
		t.Fatalf("configured trusted root caused %d network requests", got)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRequestContextHTTPClientCancelsBlockedRequest(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		close(requestStarted)
		<-req.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := (requestContextHTTPClient{ctx: ctx, client: server.Client()}).Do(req)
		result <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP request did not reach the test server")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("HTTP error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked HTTP request did not stop after caller cancellation")
	}
}

func TestCacheLockWaitHonorsContextWithoutStartingTUFFetch(t *testing.T) {
	cachePath := t.TempDir()
	lock, err := fslock.AcquireContext(context.Background(), filepath.Join(cachePath, "tuf.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	verifier := newCacheVerifier(t, cachePath, false)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	var calls atomic.Int32
	_, err = verifier.trustedRootWithLoader(ctx, func(*sigtuf.Options) (*root.TrustedRoot, error) {
		calls.Add(1)
		return readFixtureTrustedRoot(t), nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("trustedRoot error = %v, want caller deadline", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("TUF loader was called %d times while cache lock was held", got)
	}
}

func TestSeparateVerifiersSerializeTUFCacheAccess(t *testing.T) {
	cachePath := t.TempDir()
	trustedRoot := readFixtureTrustedRoot(t)
	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	allowFirstToFinish := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(allowFirstToFinish) })
	var calls atomic.Int32
	var active atomic.Int32
	var maxActive atomic.Int32
	loader := func(*sigtuf.Options) (*root.TrustedRoot, error) {
		call := calls.Add(1)
		current := active.Add(1)
		for previous := maxActive.Load(); current > previous && !maxActive.CompareAndSwap(previous, current); previous = maxActive.Load() {
		}
		defer active.Add(-1)
		if call == 1 {
			close(firstEntered)
			<-allowFirstToFinish
		} else {
			close(secondEntered)
		}
		return trustedRoot, nil
	}
	firstVerifier := newCacheVerifier(t, cachePath, false)
	secondVerifier := newCacheVerifier(t, cachePath, false)
	type loadResult struct {
		root *root.TrustedRoot
		err  error
	}
	firstResult := make(chan loadResult, 1)
	go func() {
		root, err := firstVerifier.trustedRootWithLoader(context.Background(), loader)
		firstResult <- loadResult{root: root, err: err}
	}()
	select {
	case <-firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first TUF loader did not enter")
	}
	secondStarted := make(chan struct{})
	secondResult := make(chan loadResult, 1)
	go func() {
		close(secondStarted)
		root, err := secondVerifier.trustedRootWithLoader(context.Background(), loader)
		secondResult <- loadResult{root: root, err: err}
	}()
	<-secondStarted
	select {
	case <-secondEntered:
		t.Fatal("second verifier entered TUF while the first held the cache lock")
	case <-time.After(120 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(allowFirstToFinish) })
	for name, resultChannel := range map[string]<-chan loadResult{
		"first":  firstResult,
		"second": secondResult,
	} {
		select {
		case result := <-resultChannel:
			if result.err != nil || result.root != trustedRoot {
				t.Errorf("%s verifier got root %p, error %v", name, result.root, result.err)
			}
		case <-time.After(time.Second):
			t.Errorf("%s verifier did not finish", name)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("TUF loader calls = %d, want 2", got)
	}
	if got := maxActive.Load(); got != 1 {
		t.Fatalf("maximum concurrent TUF loaders = %d, want 1", got)
	}
}

func TestDisableLocalCacheDoesNotSerializeTUFFetches(t *testing.T) {
	trustedRoot := readFixtureTrustedRoot(t)
	entered := make(chan struct{}, 2)
	allowLoadsToFinish := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(allowLoadsToFinish) })
	var active atomic.Int32
	var maxActive atomic.Int32
	loader := func(*sigtuf.Options) (*root.TrustedRoot, error) {
		current := active.Add(1)
		for previous := maxActive.Load(); current > previous && !maxActive.CompareAndSwap(previous, current); previous = maxActive.Load() {
		}
		entered <- struct{}{}
		<-allowLoadsToFinish
		active.Add(-1)
		return trustedRoot, nil
	}
	verifiers := []*nativeVerifier{
		newCacheVerifier(t, "", true),
		newCacheVerifier(t, "", true),
	}
	results := make(chan error, len(verifiers))
	for _, verifier := range verifiers {
		go func(verifier *nativeVerifier) {
			_, err := verifier.trustedRootWithLoader(context.Background(), loader)
			results <- err
		}(verifier)
	}
	for range len(verifiers) {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("cache-disabled TUF fetches were unexpectedly serialized")
		}
	}
	releaseOnce.Do(func() { close(allowLoadsToFinish) })
	for range len(verifiers) {
		if err := <-results; err != nil {
			t.Errorf("cache-disabled TUF fetch: %v", err)
		}
	}
	if got := maxActive.Load(); got != 2 {
		t.Fatalf("maximum concurrent TUF loaders = %d, want 2", got)
	}
}

func TestTUFCacheFailureCanRetryOnNextCall(t *testing.T) {
	cachePath := t.TempDir()
	verifier := newCacheVerifier(t, cachePath, false)
	trustedRoot := readFixtureTrustedRoot(t)
	failure := errors.New("temporary TUF target failure")
	var calls atomic.Int32
	loader := func(*sigtuf.Options) (*root.TrustedRoot, error) {
		if calls.Add(1) == 1 {
			return nil, failure
		}
		return trustedRoot, nil
	}
	if got, err := verifier.trustedRootWithLoader(context.Background(), loader); got != nil || !errors.Is(err, failure) {
		t.Fatalf("first fetch got root %p, error %v; want transient failure", got, err)
	}
	if got, err := verifier.trustedRootWithLoader(context.Background(), loader); got != trustedRoot || err != nil {
		t.Fatalf("second fetch got root %p, error %v; want successful retry", got, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("TUF loader calls = %d, want 2", got)
	}
}

func TestNewUsesDBCSpecificDefaultTUFCachePath(t *testing.T) {
	configPath, err := internal.GetUserConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	native := verifier.(*nativeVerifier)
	want := filepath.Join(configPath, "cache", "sigstore")
	if native.config.CachePath != want {
		t.Fatalf("default cache path = %q, want dbc-specific %q", native.config.CachePath, want)
	}
	if strings.Contains(native.config.CachePath, filepath.Join(".sigstore", "root")) {
		t.Fatalf("default cache path is shared with other Sigstore tools: %q", native.config.CachePath)
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
