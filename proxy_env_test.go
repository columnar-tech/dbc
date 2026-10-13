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

package dbc_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const proxyHelperTargetEnv = "DBC_PROXY_TEST_TARGET"

// proxyEnvVars are all the variables that influence net/http proxy selection.
var proxyEnvVars = []string{
	"HTTP_PROXY", "http_proxy",
	"HTTPS_PROXY", "https_proxy",
	"NO_PROXY", "no_proxy",
}

// TestProxyEnvHelper is not a real test. It is re-executed in a subprocess by
// TestProxyEnv because net/http reads the proxy environment variables only
// once per process, so they cannot be varied between subtests in-process.
func TestProxyEnvHelper(t *testing.T) {
	target := os.Getenv(proxyHelperTargetEnv)
	if target == "" {
		t.Skip("only runs as a subprocess of TestProxyEnv")
	}

	c, err := dbc.NewClient()
	require.NoError(t, err)

	// The outcome is observed by the parent through the proxy; the request is
	// expected to fail since the target host does not exist.
	resp, err := c.HTTPClient().Get(target)
	if err == nil {
		resp.Body.Close()
	}
}

func TestProxyEnv(t *testing.T) {
	const (
		httpTarget  = "http://dbc-cdn.columnar.tech/index.yaml"
		httpsTarget = "https://dbc-cdn.columnar.tech/index.yaml"
	)

	tests := []struct {
		name      string
		env       map[string]string
		target    string
		wantProxy bool
		wantMeth  string // method the proxy should see
	}{
		{"HTTP_PROXY used for http", map[string]string{"HTTP_PROXY": "{proxy}"}, httpTarget, true, http.MethodGet},
		{"http_proxy used for http", map[string]string{"http_proxy": "{proxy}"}, httpTarget, true, http.MethodGet},
		{"HTTPS_PROXY used for https", map[string]string{"HTTPS_PROXY": "{proxy}"}, httpsTarget, true, http.MethodConnect},
		{"https_proxy used for https", map[string]string{"https_proxy": "{proxy}"}, httpsTarget, true, http.MethodConnect},
		{"HTTPS_PROXY ignored for http", map[string]string{"HTTPS_PROXY": "{proxy}"}, httpTarget, false, ""},
		{"HTTP_PROXY ignored for https", map[string]string{"HTTP_PROXY": "{proxy}"}, httpsTarget, false, ""},
		{"no proxy variables", nil, httpsTarget, false, ""},
		{"NO_PROXY bypasses HTTP_PROXY", map[string]string{"HTTP_PROXY": "{proxy}", "NO_PROXY": "dbc-cdn.columnar.tech"}, httpTarget, false, ""},
		{"no_proxy bypasses HTTPS_PROXY", map[string]string{"HTTPS_PROXY": "{proxy}", "no_proxy": "dbc-cdn.columnar.tech"}, httpsTarget, false, ""},
		{"NO_PROXY domain suffix", map[string]string{"HTTPS_PROXY": "{proxy}", "NO_PROXY": ".invalid"}, httpsTarget, false, ""},
		{"NO_PROXY wildcard", map[string]string{"HTTPS_PROXY": "{proxy}", "NO_PROXY": "*"}, httpsTarget, false, ""},
		{"NO_PROXY for other host", map[string]string{"HTTPS_PROXY": "{proxy}", "NO_PROXY": "example.com"}, httpsTarget, true, http.MethodConnect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				mu   sync.Mutex
				seen []*http.Request
			)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, r)
				mu.Unlock()
				// Refuse everything; seeing the request is all that matters.
				w.WriteHeader(http.StatusForbidden)
			}))
			defer proxy.Close()

			cmd := exec.Command(os.Args[0], "-test.run=^TestProxyEnvHelper$")
			for _, e := range os.Environ() {
				name, _, _ := strings.Cut(e, "=")
				if !isProxyEnvVar(name) {
					cmd.Env = append(cmd.Env, e)
				}
			}
			cmd.Env = append(cmd.Env, proxyHelperTargetEnv+"="+tt.target)
			for k, v := range tt.env {
				cmd.Env = append(cmd.Env, k+"="+strings.ReplaceAll(v, "{proxy}", proxy.URL))
			}
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))

			mu.Lock()
			defer mu.Unlock()
			if !tt.wantProxy {
				assert.Empty(t, seen, "request unexpectedly went through the proxy")
				return
			}
			require.Len(t, seen, 1, "expected exactly one request via the proxy")
			assert.Equal(t, tt.wantMeth, seen[0].Method)
			assert.Equal(t, "dbc-cdn.columnar.tech", strings.Split(seen[0].Host, ":")[0])
		})
	}
}

func isProxyEnvVar(name string) bool {
	for _, v := range proxyEnvVars {
		if strings.EqualFold(name, v) {
			return true
		}
	}
	return false
}
