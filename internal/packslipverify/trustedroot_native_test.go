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
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
)

func fixtureTrustedRoot(t testing.TB) *root.TrustedRoot {
	t.Helper()
	tr, err := root.NewTrustedRootFromJSON(readFile(t, fixtureRoot))
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// tufVerifier returns a verifier that loads its root through loader, using the
// real cache-lock and fetcher plumbing.
func tufVerifier(t testing.TB, cacheDir string, client *http.Client, loader rootLoader) *nativeVerifier {
	t.Helper()
	v, err := New(Config{CacheDir: cacheDir, HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	nv := v.(*nativeVerifier)
	nv.loadRoot = loader
	return nv
}

func verifyFixture(ctx context.Context, v Verifier, t testing.TB) (*Verified, error) {
	return v.VerifyKeyless(ctx, readFile(t, fixtureBundle), githubIssuer)
}

func TestTUFRootLoadedOnEveryCall(t *testing.T) {
	tr := fixtureTrustedRoot(t)
	var calls atomic.Int32
	failNext := true
	v := tufVerifier(t, t.TempDir(), nil, func(*tuf.Options) (*root.TrustedRoot, error) {
		calls.Add(1)
		if failNext {
			failNext = false
			return nil, errors.New("tuf unavailable")
		}
		return tr, nil
	})
	// A failed load is reported, and does not poison the verifier.
	if got, err := verifyFixture(context.Background(), v, t); err == nil || got != nil {
		t.Fatalf("first call = %v, %v; want error", got, err)
	}
	for i := 0; i < 2; i++ {
		if _, err := verifyFixture(context.Background(), v, t); err != nil {
			t.Fatalf("call %d: %v", i+2, err)
		}
	}
	// Each call loaded the root again: a past success is not cached in memory.
	if got := calls.Load(); got != 3 {
		t.Fatalf("loader calls = %d, want 3", got)
	}
}

func TestTUFOptionsAreFreshPerCall(t *testing.T) {
	tr := fixtureTrustedRoot(t)
	cacheDir := t.TempDir()
	var seen []*tuf.Options
	v := tufVerifier(t, cacheDir, nil, func(o *tuf.Options) (*root.TrustedRoot, error) {
		seen = append(seen, o)
		if o.CachePath != cacheDir || o.Fetcher == nil || o.Context == nil || o.DisableLocalCache {
			t.Errorf("unexpected options: %+v", o)
		}
		return tr, nil
	})
	for i := 0; i < 2; i++ {
		if _, err := verifyFixture(context.Background(), v, t); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || seen[0] == seen[1] || seen[0].Fetcher == seen[1].Fetcher {
		t.Fatal("options or fetcher reused across calls")
	}
}

// fetchViaOptions makes the HTTP request go-tuf would make, through the
// per-call fetcher built by the verifier.
func fetchViaOptions(url string, tr *root.TrustedRoot) rootLoader {
	return func(o *tuf.Options) (*root.TrustedRoot, error) {
		if _, err := o.Fetcher.DownloadFile(url, 1<<10, time.Minute); err != nil {
			return nil, err
		}
		return tr, nil
	}
}

func TestTUFCancelDuringHTTPThenOtherCallerSucceeds(t *testing.T) {
	tr := fixtureTrustedRoot(t)
	var hold atomic.Bool
	hold.Store(true)
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hold.Load() {
			select {
			case started <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := srv.Client()
	v := tufVerifier(t, t.TempDir(), client, fetchViaOptions(srv.URL+"/metadata", tr))

	ctxA, cancelA := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := verifyFixture(ctxA, v, t)
		errCh <- err
	}()
	<-started
	cancelA()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("cancelled caller succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled caller did not return")
	}

	hold.Store(false)
	if _, err := verifyFixture(context.Background(), v, t); err != nil {
		t.Fatalf("second caller on the same verifier: %v", err)
	}
}

func TestTUFCancelledBeforeAndDuringLoad(t *testing.T) {
	tr := fixtureTrustedRoot(t)
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	v := tufVerifier(t, t.TempDir(), nil, func(*tuf.Options) (*root.TrustedRoot, error) {
		calls.Add(1)
		cancel() // the loader returns a root from a warm cache, but the caller left
		return tr, nil
	})
	if got, err := verifyFixture(ctx, v, t); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("got %v, %v; want context.Canceled", got, err)
	}
	// Already cancelled at entry: the loader is not called.
	calls.Store(0)
	if _, err := verifyFixture(ctx, v, t); !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("err = %v, loader calls = %d", err, calls.Load())
	}
}

func TestTUFCacheLockWaitIsCancellable(t *testing.T) {
	cacheDir := t.TempDir()
	lock, err := fslock.Acquire(filepath.Join(cacheDir, "tuf.lock"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Release(); err != nil {
			t.Error(err)
		}
	}()
	var calls atomic.Int32
	v := tufVerifier(t, cacheDir, nil, func(*tuf.Options) (*root.TrustedRoot, error) {
		calls.Add(1)
		return fixtureTrustedRoot(t), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	got, err := verifyFixture(ctx, v, t)
	if err == nil || got != nil {
		t.Fatalf("got %v, %v; want error", got, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > 5*time.Second || calls.Load() != 0 {
		t.Errorf("waited %v, loader calls %d", time.Since(start), calls.Load())
	}
}

func TestTUFVerifiersOnOneCacheDirAreSerialized(t *testing.T) {
	tr := fixtureTrustedRoot(t)
	cacheDir := t.TempDir()
	var active, maxActive atomic.Int32
	loader := func(*tuf.Options) (*root.TrustedRoot, error) {
		n := active.Add(1)
		for {
			m := maxActive.Load()
			if n <= m || maxActive.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		active.Add(-1)
		return tr, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		v := tufVerifier(t, cacheDir, nil, loader) // separate Verifiers, same cache
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := verifyFixture(context.Background(), v, t); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := maxActive.Load(); got != 1 {
		t.Fatalf("max concurrent loaders = %d, want 1", got)
	}
}

func TestTUFStaticRootDoesNoNetworkCacheOrLock(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "never-created")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected HTTP request")
		return nil, errors.New("no network")
	})}
	v, err := New(Config{TrustedRootJSON: readFile(t, fixtureRoot), HTTPClient: client, CacheDir: cacheDir})
	if err != nil {
		t.Fatal(err)
	}
	v.(*nativeVerifier).loadRoot = func(*tuf.Options) (*root.TrustedRoot, error) {
		t.Error("TUF loader called with a static root")
		return nil, errors.New("no tuf")
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := verifyFixture(context.Background(), v, t); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := os.Stat(cacheDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("cache directory was created (stat err = %v)", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTUFDoesNotMutateCallerHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	transport := srv.Client().Transport
	client := &http.Client{Transport: transport, Timeout: 17 * time.Second}
	before := *client

	v := tufVerifier(t, t.TempDir(), client, fetchViaOptions(srv.URL, fixtureTrustedRoot(t)))
	if _, err := verifyFixture(context.Background(), v, t); err != nil {
		t.Fatal(err)
	}
	if client.Transport != before.Transport || client.Timeout != before.Timeout ||
		client.CheckRedirect != nil || client.Jar != nil {
		t.Fatalf("caller's HTTP client was modified: %+v", client)
	}
}

func TestNewTUFConfig(t *testing.T) {
	if _, err := New(Config{CacheDir: "relative/dir"}); err == nil {
		t.Error("relative CacheDir accepted")
	}
	v, err := New(Config{CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	nv := v.(*nativeVerifier)
	if nv.httpClient == nil || nv.httpClient.Timeout == 0 {
		t.Errorf("default HTTP client has no overall timeout: %+v", nv.httpClient)
	}
	tr, ok := nv.httpClient.Transport.(*http.Transport)
	if !ok || tr.TLSHandshakeTimeout == 0 || tr.ResponseHeaderTimeout == 0 || tr.DialContext == nil {
		t.Errorf("default transport lacks timeouts: %+v", nv.httpClient.Transport)
	}
}

func TestNewDefaultCacheDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses XDG_CONFIG_HOME")
	}
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	v, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	dir := v.(*nativeVerifier).cacheDir
	if !strings.HasPrefix(dir, cfg) || !strings.HasSuffix(dir, "sigstore-tuf") {
		t.Errorf("default cache dir = %q", dir)
	}
}
