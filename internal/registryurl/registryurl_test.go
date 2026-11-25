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
