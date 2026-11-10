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

package dbc

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultClientRedirectAuthorization(t *testing.T) {
	var got string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		io.WriteString(w, "ok")
	}))
	defer target.Close()
	targetURL, err := url.Parse(target.URL)
	require.NoError(t, err)

	cases := []struct {
		name     string
		location func(origin *url.URL) string
		wantAuth bool
	}{
		{"same host and port", func(o *url.URL) string { return o.JoinPath("/final").String() }, true},
		{"same hostname other port", func(*url.URL) string { return target.URL + "/final" }, false},
		{"different host name", func(*url.URL) string { return "http://localhost:" + targetURL.Port() + "/final" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got = ""
			var origin *httptest.Server
			origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/final" {
					got = r.Header.Get("Authorization")
					io.WriteString(w, "ok")
					return
				}
				originURL, _ := url.Parse(origin.URL)
				http.Redirect(w, r, tc.location(originURL), http.StatusFound)
			}))
			defer origin.Close()
			originURL, err := url.Parse(origin.URL)
			require.NoError(t, err)

			c, err := NewClient(
				WithBaseURL(origin.URL),
				WithCredentialResolver(func(*url.URL) (*auth.Credential, error) {
					return &auth.Credential{Type: auth.TypeApiKey, ApiKey: "k", Token: "secret", RegistryURL: auth.Uri(*originURL)}, nil
				}),
			)
			require.NoError(t, err)

			rsp, err := c.makeRequest(t.Context(), origin.URL+"/start")
			require.NoError(t, err)
			defer rsp.Body.Close()
			if tc.wantAuth {
				assert.Equal(t, "Bearer secret", got)
			} else {
				assert.Empty(t, got)
			}
		})
	}
}

func TestDefaultClientTooManyRedirects(t *testing.T) {
	var srv *httptest.Server
	hops := 0
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, fmt.Sprintf("%s/hop%d", srv.URL, hops), http.StatusFound)
	}))
	defer srv.Close()

	c, err := NewClient(WithBaseURL(srv.URL))
	require.NoError(t, err)
	rsp, err := c.makeRequest(t.Context(), srv.URL+"/start")
	if rsp != nil {
		rsp.Body.Close()
	}
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "stopped after 10 redirects"), err.Error())
}

func TestCheckRedirectRefererOriginScope(t *testing.T) {
	t.Run("same origin preserves headers", func(t *testing.T) {
		var srv *httptest.Server
		var gotAuth, gotReferer string
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/final" {
				gotAuth = r.Header.Get("Authorization")
				gotReferer = r.Referer()
				return
			}
			http.Redirect(w, r, srv.URL+"/final", http.StatusFound)
		}))
		defer srv.Close()

		client := &http.Client{CheckRedirect: checkRedirect}
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/start?token=query-secret", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer secret")
		rsp, err := client.Do(req)
		require.NoError(t, err)
		rsp.Body.Close()

		assert.Equal(t, "Bearer secret", gotAuth)
		assert.Equal(t, srv.URL+"/start?token=query-secret", gotReferer)
	})

	t.Run("cross origin strips query-bearing referer and authorization", func(t *testing.T) {
		var gotAuth, gotReferer string
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			gotReferer = r.Referer()
		}))
		defer target.Close()

		var source *httptest.Server
		source = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/final", http.StatusFound)
		}))
		defer source.Close()

		client := &http.Client{CheckRedirect: checkRedirect}
		req, err := http.NewRequest(http.MethodGet, source.URL+"/start?token=query-secret", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer secret")
		rsp, err := client.Do(req)
		require.NoError(t, err)
		rsp.Body.Close()

		assert.Empty(t, gotAuth)
		assert.Empty(t, gotReferer)
	})

	for _, explicitReferer := range []bool{false, true} {
		t.Run(fmt.Sprintf("headers stay scoped within foreign origin explicitReferer=%t", explicitReferer), func(t *testing.T) {
			var finalAuth, finalReferer string
			var target *httptest.Server
			target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/foreign" {
					http.Redirect(w, r, target.URL+"/final", http.StatusFound)
					return
				}
				finalAuth = r.Header.Get("Authorization")
				finalReferer = r.Referer()
			}))
			defer target.Close()

			var source *httptest.Server
			source = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/foreign", http.StatusFound)
			}))
			defer source.Close()

			client := &http.Client{CheckRedirect: checkRedirect}
			req, err := http.NewRequest(http.MethodGet, source.URL+"/start", nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer secret")
			if explicitReferer {
				req.Header.Set("Referer", source.URL+"/private?token=referer-secret")
			}
			rsp, err := client.Do(req)
			require.NoError(t, err)
			rsp.Body.Close()

			assert.Empty(t, finalAuth)
			assert.Empty(t, finalReferer)
		})
	}
}

func TestCheckRedirectOriginNormalization(t *testing.T) {
	cases := []struct {
		name  string
		a     string
		b     string
		equal bool
	}{
		{"hostname case and default HTTP port", "http://EXAMPLE.com/path", "http://example.COM:80/other", true},
		{"default HTTPS port", "https://example.com/path", "https://EXAMPLE.com:443/other", true},
		{"nondefault port", "http://example.com:8080/", "http://example.com/", false},
		{"hostname", "http://example.com/", "http://other.example/", false},
		{"scheme", "http://example.com/", "https://example.com/", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := url.Parse(tc.a)
			require.NoError(t, err)
			b, err := url.Parse(tc.b)
			require.NoError(t, err)
			assert.Equal(t, tc.equal, originOf(a) == originOf(b))
		})
	}
}

func TestCheckRedirectRejectsHTTPSDowngrade(t *testing.T) {
	var targetRequests int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests++
	}))
	defer target.Close()

	var source *httptest.Server
	source = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/final", http.StatusFound)
	}))
	defer source.Close()

	client := source.Client()
	client.CheckRedirect = checkRedirect
	rsp, err := client.Get(source.URL + "/start")
	if rsp != nil {
		rsp.Body.Close()
	}
	require.ErrorContains(t, err, "refusing HTTPS to HTTP redirect")
	assert.Zero(t, targetRequests)
}

func TestCheckRedirectHTTPSUpgradeStripsHeaders(t *testing.T) {
	var gotAuth, gotReferer string
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotReferer = r.Referer()
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/final", http.StatusFound)
	}))
	defer source.Close()

	client := target.Client()
	client.CheckRedirect = checkRedirect
	req, err := http.NewRequest(http.MethodGet, source.URL+"/start?token=query-secret", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer secret")
	rsp, err := client.Do(req)
	require.NoError(t, err)
	rsp.Body.Close()
	assert.Empty(t, gotAuth)
	assert.Empty(t, gotReferer)
}

func TestCheckRedirectRejectsHTTPSDowngradeAfterUpgrade(t *testing.T) {
	var targetRequests int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests++
	}))
	defer target.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/final", http.StatusFound)
	}))
	defer secure.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secure.URL+"/intermediate", http.StatusFound)
	}))
	defer source.Close()

	client := secure.Client()
	client.CheckRedirect = checkRedirect
	rsp, err := client.Get(source.URL + "/start")
	if rsp != nil {
		rsp.Body.Close()
	}
	require.ErrorContains(t, err, "refusing HTTPS to HTTP redirect")
	assert.Zero(t, targetRequests)
}
