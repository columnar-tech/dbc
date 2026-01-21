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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetProxy(t *testing.T) {
	// Save original transport
	originalTransport := DefaultClient.Transport

	t.Cleanup(func() {
		DefaultClient.Transport = originalTransport
	})

	t.Run("valid proxy URL", func(t *testing.T) {
		err := SetProxy("http://proxy.example.com:8080")
		require.NoError(t, err)

		// Check that transport is set
		ua, ok := DefaultClient.Transport.(*uaRoundTripper)
		require.True(t, ok, "transport should be uaRoundTripper")

		transport, ok := ua.RoundTripper.(*http.Transport)
		require.True(t, ok, "inner transport should be http.Transport")

		require.NotNil(t, transport.Proxy, "proxy should be set")
	})

	t.Run("empty proxy", func(t *testing.T) {
		err := SetProxy("")
		require.NoError(t, err)

		ua, ok := DefaultClient.Transport.(*uaRoundTripper)
		require.True(t, ok)

		// Should use default transport
		assert.Equal(t, http.DefaultTransport, ua.RoundTripper)
	})

	t.Run("invalid proxy URL", func(t *testing.T) {
		err := SetProxy("://invalid")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid proxy URL")
	})
}
