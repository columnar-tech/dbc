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

package packslip

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoveryRejectsHTTPSRedirectToLoopbackHTTP(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("private response"))
	}))
	defer target.Close()

	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/private", http.StatusFound)
	}))
	defer origin.Close()
	discovery, err := NewGitHubPackslipDiscovery(origin.Client(), origin.URL, origin.URL)
	require.NoError(t, err)
	_, _, err = discovery.get(context.Background(), origin.URL+"/metadata", false)
	require.ErrorContains(t, err, "HTTPS-to-HTTP downgrade")
	require.Zero(t, targetHits.Load(), "the redirected loopback endpoint must not receive a request")
}

func TestDiscoveryAllowsRedirectWithinConfiguredLoopbackHTTPTestEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("test metadata"))
	}))
	defer server.Close()
	discovery, err := NewGitHubPackslipDiscovery(server.Client(), server.URL, server.URL)
	require.NoError(t, err)
	body, finalURL, err := discovery.get(context.Background(), server.URL+"/start", false)
	require.NoError(t, err)
	require.Equal(t, []byte("test metadata"), body)
	require.Equal(t, server.URL+"/final", finalURL)

	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("unconfigured endpoint"))
	}))
	defer outside.Close()
	_, _, err = discovery.get(context.Background(), outside.URL+"/metadata", false)
	require.ErrorContains(t, err, "configured loopback test endpoint")
}

func TestDiscoveryComposesCallerRedirectPolicy(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("target"))
	}))
	defer target.Close()

	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/redirected", http.StatusFound)
	}))
	defer origin.Close()
	client := origin.Client()
	callerErr := errors.New("caller redirect policy rejected request")
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return callerErr }
	discovery, err := NewGitHubPackslipDiscovery(client, origin.URL, origin.URL)
	require.NoError(t, err)
	_, _, err = discovery.get(context.Background(), origin.URL+"/metadata", false)
	require.ErrorIs(t, err, callerErr)
	require.Zero(t, targetHits.Load(), "the caller policy must still control safe redirects")
}

func TestDiscoveryRevalidatesRedirectAfterCallerPolicyMutation(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("mutated target"))
	}))
	defer target.Close()

	safeTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("safe target"))
	}))
	defer safeTarget.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, safeTarget.URL+"/redirected", http.StatusFound)
	}))
	defer origin.Close()
	client := origin.Client()
	client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
		request.URL.Scheme = "http"
		request.URL.Host = target.Listener.Addr().String()
		return nil
	}
	discovery, err := NewGitHubPackslipDiscovery(client, origin.URL, origin.URL)
	require.NoError(t, err)
	_, _, err = discovery.get(context.Background(), origin.URL+"/metadata", false)
	require.ErrorContains(t, err, "HTTPS-to-HTTP downgrade")
	require.Zero(t, targetHits.Load(), "a caller mutation must not bypass redirect validation")
}
