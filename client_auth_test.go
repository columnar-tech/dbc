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
	"net/url"
	"path/filepath"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientWithCredential(t *testing.T) {
	const token = "test-injected-token"

	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("drivers: []\n"))
	}))
	defer srv.Close()

	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	cred := &auth.Credential{
		Type:        auth.TypeApiKey,
		ApiKey:      "unused-api-key",
		Token:       token,
		RegistryURL: auth.Uri(*srvURL),
	}

	c, err := dbc.NewClient(
		dbc.WithHTTPClient(&http.Client{}),
		dbc.WithBaseURL(srv.URL),
		dbc.WithCredential(cred),
	)
	require.NoError(t, err)

	_, _ = c.Search("")

	assert.Equal(t, "Bearer "+token, gotAuthHeader)
}

func TestClientWithAuthFromFilesystem(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/yaml")
		w.Write([]byte("drivers: []\n"))
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	restore := auth.SetCredPathForTesting(filepath.Join(tmpDir, "credentials.toml"))
	defer restore()
	auth.ResetCredentialsForTesting()

	c, err := dbc.NewClient(
		dbc.WithHTTPClient(&http.Client{}),
		dbc.WithBaseURL(srv.URL),
		dbc.WithAuthFromFilesystem(),
	)
	require.NoError(t, err)

	_, _ = c.Search("")

	assert.Empty(t, gotAuthHeader)
}

func TestClientLogin(t *testing.T) {
	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")
	restore := auth.SetCredPathForTesting(credPath)
	defer restore()
	auth.ResetCredentialsForTesting()

	c, err := dbc.NewClient()
	require.NoError(t, err)

	u, err := url.Parse("https://login-test.example.com")
	require.NoError(t, err)
	cred := &auth.Credential{
		Type:        auth.TypeApiKey,
		ApiKey:      "my-api-key",
		Token:       "my-token",
		RegistryURL: auth.Uri(*u),
	}

	err = c.Login(cred)
	require.NoError(t, err)

	auth.ResetCredentialsForTesting()
	found, err := auth.GetCredentials(u)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "my-api-key", found.ApiKey)

	cred2 := &auth.Credential{
		Type:        auth.TypeApiKey,
		ApiKey:      "updated-key",
		Token:       "updated-token",
		RegistryURL: auth.Uri(*u),
	}
	err = c.Login(cred2)
	require.NoError(t, err)

	auth.ResetCredentialsForTesting()
	updated, err := auth.GetCredentials(u)
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, "updated-key", updated.ApiKey)
}

func TestClientLogout(t *testing.T) {
	tmpDir := t.TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")
	restore := auth.SetCredPathForTesting(credPath)
	defer restore()
	auth.ResetCredentialsForTesting()

	c, err := dbc.NewClient()
	require.NoError(t, err)

	u, err := url.Parse("https://logout-test.example.com")
	require.NoError(t, err)
	cred := &auth.Credential{
		Type:        auth.TypeApiKey,
		ApiKey:      "my-api-key",
		Token:       "my-token",
		RegistryURL: auth.Uri(*u),
	}

	require.NoError(t, c.Login(cred))

	err = c.Logout(u)
	require.NoError(t, err)

	auth.ResetCredentialsForTesting()
	found, err := auth.GetCredentials(u)
	require.NoError(t, err)
	assert.Nil(t, found)

	u2, err := url.Parse("https://not-registered.example.com")
	require.NoError(t, err)
	err = c.Logout(u2)
	assert.Error(t, err)
}
