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
