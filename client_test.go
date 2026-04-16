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
	"net/url"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientDefaults(t *testing.T) {
	c, err := dbc.NewClient()
	require.NoError(t, err)
	assert.NotNil(t, c)
	assert.NotNil(t, c.HTTPClient())
	assert.Len(t, c.Registries(), 2)
}

func TestNewClientWithHTTPClient(t *testing.T) {
	custom := &http.Client{}
	c, err := dbc.NewClient(dbc.WithHTTPClient(custom))
	require.NoError(t, err)
	assert.Same(t, custom, c.HTTPClient())
}

func TestNewClientWithRegistries(t *testing.T) {
	u, _ := url.Parse("https://example.com")
	regs := []dbc.Registry{{BaseURL: u}}
	c, err := dbc.NewClient(dbc.WithRegistries(regs))
	require.NoError(t, err)
	assert.Equal(t, regs, c.Registries())
}

func TestNewClientWithBaseURL(t *testing.T) {
	c, err := dbc.NewClient(dbc.WithBaseURL("https://custom.example.com"))
	require.NoError(t, err)
	regs := c.Registries()
	require.Len(t, regs, 1)
	assert.Equal(t, "https://custom.example.com", regs[0].BaseURL.String())
}

func TestNewClientWithUserAgent(t *testing.T) {
	c, err := dbc.NewClient(dbc.WithUserAgent("custom-agent/1.0"))
	require.NoError(t, err)
	assert.Equal(t, "custom-agent/1.0", c.UserAgent())
}
