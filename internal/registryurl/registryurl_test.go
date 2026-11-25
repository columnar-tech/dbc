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

package registryurl

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthority(t *testing.T) {
	tests := []struct{ in, scheme, host string }{
		{"https://Example.COM/x", "https", "example.com"},
		{"HTTPS://example.com:443", "https", "example.com"},
		{"http://example.com:80", "http", "example.com"},
		// Only the default port of the scheme is dropped.
		{"http://example.com:443", "http", "example.com:443"},
		{"https://example.com:8443", "https", "example.com:8443"},
		{"https://[::1]:443", "https", "[::1]"},
		{"https://user:pw@example.com:443", "https", "example.com"},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.in)
		require.NoError(t, err)
		scheme, host := Authority(u)
		assert.Equal(t, tt.scheme, scheme, tt.in)
		assert.Equal(t, tt.host, host, tt.in)
	}
}

func TestCheckPath(t *testing.T) {
	for _, ok := range []string{"https://h", "https://h/", "https://h/m", "https://h/m/", "https://h/a/b", "https://h/a%20b", "https://h/v1.0/..x"} {
		u, err := url.Parse(ok)
		require.NoError(t, err)
		assert.NoError(t, CheckPath(u), ok)
	}
	for _, bad := range []string{"https://h/.", "https://h/a/..", "https://h/a/../b", "https://h/./a", "https://h//a", "https://h/a//b", "https://h/a//",
		"https://h/%2e", "https://h/a/%2E%2e", "https://h/a%2fb", "https://h/%zz"} {
		u, err := url.Parse(bad)
		if err != nil {
			continue
		}
		assert.Error(t, CheckPath(u), bad)
	}
}

func TestEscapedPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://h/drivers", "/drivers"},
		// Escaped unreserved characters are decoded.
		{"https://h/%64rivers", "/drivers"},
		{"https://h/DRIV%65rs", "/DRIVers"},
		{"https://h/a%2Db%2E%5Fc%7E", "/a-b._c~"},
		// Other escapes keep their meaning, with upper-case hex.
		{"https://h/a%3ab", "/a%3Ab"},
		{"https://h/a%3Ab", "/a%3Ab"},
		{"https://h/a:b", "/a:b"},
		{"https://h/a%40b", "/a%40b"},
		{"https://h/a@b", "/a@b"},
		{"https://h/a%2fb", "/a%2Fb"},
		{"https://h/a%20b", "/a%20b"},
		{"https://h/caf%c3%a9", "/caf%C3%A9"},
		// Nothing is decoded twice.
		{"https://h/a%2564", "/a%2564"},
		{"https://h", ""},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.in)
		require.NoError(t, err)
		assert.Equal(t, tt.want, EscapedPath(u), tt.in)
	}
}
