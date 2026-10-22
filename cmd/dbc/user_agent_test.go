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
	"runtime"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultUserAgent() string {
	return fmt.Sprintf("dbc-cli/%s (%s; %s)", dbc.Version, runtime.GOOS, runtime.GOARCH)
}

func TestUserAgentFlagParsing(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"short", []string{"-u", "my-tool/2.0", "search"}, "my-tool/2.0"},
		{"long", []string{"--user-agent", "my-tool/2.0", "search"}, "my-tool/2.0"},
		{"long equals", []string{"--user-agent=with spaces (x)", "search"}, "with spaces (x)"},
		{"absent", []string{"search"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var args cmds
			p, err := newParser(&args)
			require.NoError(t, err)
			require.NoError(t, p.Parse(tt.argv))
			assert.Equal(t, tt.want, args.UserAgent)
		})
	}
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
