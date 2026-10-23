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

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultUserAgent() string {
	return fmt.Sprintf("dbc-cli/%s (%s; %s)", dbc.Version, runtime.GOOS, runtime.GOARCH)
}

// TestNewDBCClientSendsUserAgent verifies the header that actually reaches the
// server, with and without a custom suffix.
func TestNewDBCClientSendsUserAgent(t *testing.T) {
	tests := []struct {
		name   string
		suffix string
		want   string
	}{
		{"default", "", defaultUserAgent()},
		{"custom", "my-tool/2.0", defaultUserAgent() + " my-tool/2.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("User-Agent")
			}))
			t.Cleanup(srv.Close)

			t.Setenv("DBC_BASE_URL", srv.URL)
			// Keep the expected header independent of the runner's CI env.
			t.Setenv("CI", "")
			prev := userAgentSuffix
			t.Cleanup(func() { userAgentSuffix = prev })
			userAgentSuffix = tt.suffix

			c, err := newDBCClient(nil, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.want, c.UserAgent())

			resp, err := c.HTTPClient().Get(srv.URL)
			require.NoError(t, err)
			resp.Body.Close()
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestSetUserAgentSuffixCoversAuthAndDownloads verifies that the HTTP paths
// outside dbcClient — the auth client and package downloads via
// PkgInfo.DownloadPackage — also send the suffix.
func TestSetUserAgentSuffixCoversAuthAndDownloads(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte("pkg"))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CI", "")

	prevSuffix, prevDefault := userAgentSuffix, dbc.DefaultClient
	t.Cleanup(func() { userAgentSuffix, dbc.DefaultClient = prevSuffix, prevDefault })
	setUserAgentSuffix("my-tool/2.0")
	want := defaultUserAgent() + " my-tool/2.0"

	resp, err := authHTTPClient().Get(srv.URL)
	require.NoError(t, err)
	resp.Body.Close()

	u, err := url.Parse(srv.URL + "/pkg.tar.gz")
	require.NoError(t, err)
	f, err := dbc.PkgInfo{Path: u}.DownloadPackage(func(int64, int64) {})
	require.NoError(t, err)
	f.Close()
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(f.Name())) })

	assert.Equal(t, []string{want, want}, got)
}
