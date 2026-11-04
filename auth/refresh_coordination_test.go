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

package auth

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const refreshChildEnv = "DBC_AUTH_REFRESH_CHILD_CRED_PATH"

// tokenServer counts API-key refreshes and answers each, after delay, with a
// distinct access token.
func tokenServer(t *testing.T, delay time.Duration) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		time.Sleep(delay)
		fmt.Fprintf(w, `{"access_token": "token-%d"}`, n)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func apiKeyCredential(t *testing.T, authURI string) Credential {
	t.Helper()
	u, err := url.Parse(authURI)
	require.NoError(t, err)
	return Credential{
		Type:        TypeApiKey,
		RegistryURL: Uri(url.URL{Scheme: "https", Host: "registry.example.test"}),
		AuthURI:     Uri(*u),
		ApiKey:      "api-key",
	}
}

// TestRefreshChild is the body of the processes TestConcurrentProcessesRefreshOnce
// starts: it asks the shared store for a token and prints it.
func TestRefreshChild(t *testing.T) {
	path := os.Getenv(refreshChildEnv)
	if path == "" {
		t.Skip("only runs as a subprocess of TestConcurrentProcessesRefreshOnce")
	}
	SetCredPathForTesting(path)
	cred, err := GetCredentials(&url.URL{Scheme: "https", Host: "registry.example.test"})
	require.NoError(t, err)
	require.NotNil(t, cred)
	fmt.Printf("TOKEN=%s\n", cred.GetAuthToken(context.Background()))
}

// Processes that find the same credential without an access token must not
// each refresh it: one refreshes while holding the store's lock, and the
// others, deciding only after they hold it, use the token it stored.
func TestConcurrentProcessesRefreshOnce(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("js/wasm cannot start subprocesses")
	}
	resetCredState(t)
	withTempCredPath(t)
	srv, hits := tokenServer(t, 300*time.Millisecond)
	writeCredFile(t, apiKeyCredential(t, srv.URL))

	const processes = 4
	tokens := make([]string, processes)
	var wg sync.WaitGroup
	for i := range processes {
		wg.Go(func() {
			cmd := exec.Command(os.Args[0], "-test.run=^TestRefreshChild$", "-test.count=1")
			cmd.Env = append(os.Environ(), refreshChildEnv+"="+credPath)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Run(); err != nil {
				t.Errorf("child %d: %v\n%s", i, err, out.String())
				return
			}
			for line := range strings.Lines(out.String()) {
				if token, ok := strings.CutPrefix(strings.TrimSpace(line), "TOKEN="); ok {
					tokens[i] = token
				}
			}
		})
	}
	wg.Wait()

	assert.Equal(t, int32(1), hits.Load(), "token endpoint requests")
	for i, token := range tokens {
		assert.Equal(t, "token-1", token, "child %d", i)
	}
	creds, err := loadCreds()
	require.NoError(t, err)
	require.Len(t, creds, 1)
	assert.Equal(t, "token-1", creds[0].Token)
}

// A process that holds the store's lock for too long must not stall the
// others: after credentialLockTimeout they refresh on their own, without
// writing, since their decision did not come from a read under the lock.
func TestRefreshDoesNotWaitForeverOnAStuckHolder(t *testing.T) {
	resetCredState(t)
	withTempCredPath(t)
	orig := credentialLockTimeout
	credentialLockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { credentialLockTimeout = orig })
	srv, hits := tokenServer(t, 0)
	writeCredFile(t, apiKeyCredential(t, srv.URL))
	cred, err := GetCredentials(&url.URL{Scheme: "https", Host: "registry.example.test"})
	require.NoError(t, err)

	held, err := fslock.Acquire(credPath+".lock", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Release() })

	start := time.Now()
	assert.Equal(t, "token-1", cred.GetAuthToken(context.Background()))
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Equal(t, int32(1), hits.Load())
	creds, err := loadCreds()
	require.NoError(t, err)
	assert.Empty(t, creds[0].Token, "wrote a refresh decided without the lock")
}

// Waiting for another process's lock must stop when the caller's context
// does, not only after credentialLockTimeout.
func TestRefreshHonorsContextWhileWaitingForTheLock(t *testing.T) {
	resetCredState(t)
	withTempCredPath(t)
	orig := credentialLockTimeout
	credentialLockTimeout = 3 * time.Second
	t.Cleanup(func() { credentialLockTimeout = orig })
	srv, hits := tokenServer(t, 0)
	writeCredFile(t, apiKeyCredential(t, srv.URL))
	cred, err := GetCredentials(&url.URL{Scheme: "https", Host: "registry.example.test"})
	require.NoError(t, err)
	held, err := fslock.Acquire(credPath+".lock", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Release() })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = cred.RefreshAndPersist(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), time.Second)
	assert.Zero(t, hits.Load())
}

// If the store cannot be read, the refresh does not depend on it, so it must
// not run while holding the store's lock.
func TestRefreshOfAnUnreadableStoreDoesNotHoldTheLock(t *testing.T) {
	resetCredState(t)
	withTempCredPath(t)
	var lockFree atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probe, err := fslock.Acquire(credPath+".lock", 0); err == nil {
			lockFree.Store(true)
			_ = probe.Release()
		}
		fmt.Fprint(w, `{"access_token": "token-1"}`)
	}))
	t.Cleanup(srv.Close)
	writeCredFile(t, apiKeyCredential(t, srv.URL))
	cred, err := GetCredentials(&url.URL{Scheme: "https", Host: "registry.example.test"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(credPath, []byte("[[broken"), 0o600))

	assert.Equal(t, "token-1", cred.GetAuthToken(context.Background()))
	assert.True(t, lockFree.Load(), "the store's lock was held during the refresh")
}
