// Copyright 2025 Columnar Technologies Inc.
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

package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestType_UnmarshalText(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Type
		wantErr bool
	}{
		{
			name:    "valid apikey type",
			input:   "apikey",
			want:    TypeApiKey,
			wantErr: false,
		},
		{
			name:    "valid oauth type",
			input:   "oauth",
			want:    TypeToken,
			wantErr: false,
		},
		{
			name:    "invalid type",
			input:   "invalid",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var a Type
			err := a.UnmarshalText([]byte(tt.input))
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid auth type")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, a)
			}
		})
	}
}

func TestUri_MarshalUnmarshalText(t *testing.T) {
	tests := []struct {
		name string
		uri  string
	}{
		{
			name: "http url",
			uri:  "http://example.com",
		},
		{
			name: "https url",
			uri:  "https://example.com/path",
		},
		{
			name: "url with port",
			uri:  "https://example.com:8080/path",
		},
		{
			name: "url with query",
			uri:  "https://example.com/path?key=value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.uri)
			require.NoError(t, err)

			uri := (*Uri)(u)

			// Test Marshal
			marshaled, err := uri.MarshalText()
			require.NoError(t, err)

			// Test Unmarshal
			var unmarshaled Uri
			err = unmarshaled.UnmarshalText(marshaled)
			require.NoError(t, err)

			// Verify they're equal
			assert.Equal(t, uri.String(), unmarshaled.String())
		})
	}
}

func TestUri_String(t *testing.T) {
	u, _ := url.Parse("https://example.com/path")
	uri := (*Uri)(u)
	assert.Equal(t, "https://example.com/path", uri.String())
}

func TestCredential_GetAuthToken(t *testing.T) {
	t.Run("returns existing token", func(t *testing.T) {
		cred := &Credential{
			Token: "existing-token",
		}
		assert.Equal(t, "existing-token", cred.GetAuthToken())
	})

	t.Run("returns empty string when refresh fails", func(t *testing.T) {
		// Save original credPath and restore after test
		origCredPath := credPath
		defer func() { credPath = origCredPath }()

		// Set temporary credPath
		tmpDir := t.TempDir()
		credPath = filepath.Join(tmpDir, "credentials.toml")

		cred := &Credential{
			Type:   TypeApiKey,
			ApiKey: "test-key",
		}
		cred.AuthURI = Uri(url.URL{Scheme: "http", Host: "invalid-host-xyz-123.local"})

		token := cred.GetAuthToken()
		assert.Equal(t, "", token)
	})
}

func TestCredential_Refresh_ApiKey(t *testing.T) {
	t.Run("successful refresh with apikey", func(t *testing.T) {
		// Create test server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "test-api-key", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"access_token": "new-token"}`))
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		cred := &Credential{
			Type:    TypeApiKey,
			AuthURI: Uri(*serverURL),
			ApiKey:  "test-api-key",
		}

		success := cred.Refresh()
		assert.True(t, success)
		assert.Equal(t, "new-token", cred.Token)
	})

	t.Run("failed refresh with apikey - server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		cred := &Credential{
			Type:    TypeApiKey,
			AuthURI: Uri(*serverURL),
			ApiKey:  "invalid-key",
		}

		success := cred.Refresh()
		assert.False(t, success)
	})

	t.Run("failed refresh with apikey - invalid json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`invalid json`))
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		cred := &Credential{
			Type:    TypeApiKey,
			AuthURI: Uri(*serverURL),
			ApiKey:  "test-api-key",
		}

		success := cred.Refresh()
		assert.False(t, success)
	})
}

func TestLoadCreds(t *testing.T) {
	t.Run("load valid credentials file", func(t *testing.T) {
		// Save original credPath and restore after test
		origCredPath := credPath
		defer func() { credPath = origCredPath }()

		// Create temporary credentials file
		tmpDir := t.TempDir()
		credPath = filepath.Join(tmpDir, "credentials.toml")

		testCreds := struct {
			Credentials []Credential `toml:"credentials"`
		}{
			Credentials: []Credential{
				{
					Type:   TypeApiKey,
					ApiKey: "test-key",
				},
			},
		}

		// Write test credentials
		require.NoError(t, os.MkdirAll(filepath.Dir(credPath), 0o700))
		f, err := os.Create(credPath)
		require.NoError(t, err)
		require.NoError(t, toml.NewEncoder(f).Encode(testCreds))
		f.Close()

		// Load credentials
		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Len(t, creds, 1)
		assert.Equal(t, TypeApiKey, creds[0].Type)
		assert.Equal(t, "test-key", creds[0].ApiKey)
	})

	t.Run("return empty slice when file does not exist", func(t *testing.T) {
		// Save original credPath and restore after test
		origCredPath := credPath
		defer func() { credPath = origCredPath }()

		tmpDir := t.TempDir()
		credPath = filepath.Join(tmpDir, "nonexistent.toml")

		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Len(t, creds, 0)
	})

	t.Run("return error when file is invalid", func(t *testing.T) {
		// Save original credPath and restore after test
		origCredPath := credPath
		defer func() { credPath = origCredPath }()

		tmpDir := t.TempDir()
		credPath = filepath.Join(tmpDir, "invalid.toml")

		// Write invalid TOML
		require.NoError(t, os.WriteFile(credPath, []byte("invalid toml content [[["), 0o600))

		_, err := loadCreds()
		assert.Error(t, err)
	})
}

func TestGetCredentials(t *testing.T) {
	// Save original values and restore after test
	origCredPath := credPath
	origLoadedCredentials := loadedCredentials
	defer func() {
		credPath = origCredPath
		loadedCredentials = origLoadedCredentials
	}()

	// Reset loaded state
	loaded = sync.Once{}

	t.Run("get existing credentials", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath = filepath.Join(tmpDir, "credentials.toml")

		u, _ := url.Parse("https://example.com")
		testCreds := struct {
			Credentials []Credential `toml:"credentials"`
		}{
			Credentials: []Credential{
				{
					Type:        TypeApiKey,
					RegistryURL: Uri(*u),
					ApiKey:      "test-key",
				},
			},
		}

		require.NoError(t, os.MkdirAll(filepath.Dir(credPath), 0o700))
		f, err := os.Create(credPath)
		require.NoError(t, err)
		require.NoError(t, toml.NewEncoder(f).Encode(testCreds))
		f.Close()

		cred, err := GetCredentials(u)
		require.NoError(t, err)
		require.NotNil(t, cred)
		assert.Equal(t, "test-key", cred.ApiKey)
	})

	// Reset for next test
	loaded = sync.Once{}

	t.Run("return nil when credentials not found", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath = filepath.Join(tmpDir, "credentials.toml")

		// Create empty credentials file
		testCreds := struct {
			Credentials []Credential `toml:"credentials"`
		}{
			Credentials: []Credential{},
		}

		require.NoError(t, os.MkdirAll(filepath.Dir(credPath), 0o700))
		f, err := os.Create(credPath)
		require.NoError(t, err)
		require.NoError(t, toml.NewEncoder(f).Encode(testCreds))
		f.Close()

		u, _ := url.Parse("https://notfound.com")
		cred, err := GetCredentials(u)
		require.NoError(t, err)
		assert.Nil(t, cred)
	})
}

func TestAddCredential(t *testing.T) {
	// Save original values and restore after test
	origCredPath := credPath
	origLoadedCredentials := loadedCredentials
	defer func() {
		credPath = origCredPath
		loadedCredentials = origLoadedCredentials
	}()

	// Reset loaded state
	loaded = sync.Once{}

	t.Run("add new credential", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath = filepath.Join(tmpDir, "credentials.toml")

		u, _ := url.Parse("https://example.com")
		newCred := Credential{
			Type:        TypeApiKey,
			RegistryURL: Uri(*u),
			ApiKey:      "new-key",
		}

		err := AddCredential(newCred, false)
		require.NoError(t, err)

		// Verify it was added
		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Len(t, creds, 1)
		assert.Equal(t, "new-key", creds[0].ApiKey)
	})

	// Reset for next test
	loaded = sync.Once{}
	loadedCredentials = nil

	t.Run("return error when credential already exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath = filepath.Join(tmpDir, "credentials.toml")

		u, _ := url.Parse("https://example.com")
		cred := Credential{
			Type:        TypeApiKey,
			RegistryURL: Uri(*u),
			ApiKey:      "test-key",
		}

		// Add first time
		err := AddCredential(cred, false)
		require.NoError(t, err)

		// Try to add again
		err = AddCredential(cred, false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already exist")
	})

	// Reset for next test
	loaded = sync.Once{}
	loadedCredentials = nil

	t.Run("overwrite existing credential when allowOverwrite is true", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath = filepath.Join(tmpDir, "credentials.toml")

		u, _ := url.Parse("https://example.com")
		originalCred := Credential{
			Type:        TypeApiKey,
			RegistryURL: Uri(*u),
			ApiKey:      "original-key",
		}

		// Add first credential
		err := AddCredential(originalCred, false)
		require.NoError(t, err)

		// Verify original credential was added
		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Len(t, creds, 1)
		assert.Equal(t, "original-key", creds[0].ApiKey)

		// Overwrite with new credential
		updatedCred := Credential{
			Type:        TypeApiKey,
			RegistryURL: Uri(*u),
			ApiKey:      "updated-key",
		}
		err = AddCredential(updatedCred, true)
		require.NoError(t, err)

		// Verify credential was overwritten
		creds, err = loadCreds()
		require.NoError(t, err)
		assert.Len(t, creds, 1)
		assert.Equal(t, "updated-key", creds[0].ApiKey)
	})
}

func TestRemoveCredential(t *testing.T) {
	// Save original values and restore after test
	origCredPath := credPath
	origLoadedCredentials := loadedCredentials
	defer func() {
		credPath = origCredPath
		loadedCredentials = origLoadedCredentials
	}()

	// Reset loaded state
	loaded = sync.Once{}

	t.Run("remove existing credential", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath = filepath.Join(tmpDir, "credentials.toml")

		u, _ := url.Parse("https://example.com")
		cred := Credential{
			Type:        TypeApiKey,
			RegistryURL: Uri(*u),
			ApiKey:      "test-key",
		}

		// Add credential
		err := AddCredential(cred, false)
		require.NoError(t, err)

		// Remove it
		err = RemoveCredential(Uri(*u))
		require.NoError(t, err)

		// Verify it was removed
		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Len(t, creds, 0)
	})

	// Reset for next test
	loaded = sync.Once{}
	loadedCredentials = nil

	t.Run("return error when credential not found", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath = filepath.Join(tmpDir, "credentials.toml")

		// Create empty credentials file
		testCreds := struct {
			Credentials []Credential `toml:"credentials"`
		}{
			Credentials: []Credential{},
		}

		require.NoError(t, os.MkdirAll(filepath.Dir(credPath), 0o700))
		f, err := os.Create(credPath)
		require.NoError(t, err)
		require.NoError(t, toml.NewEncoder(f).Encode(testCreds))
		f.Close()

		u, _ := url.Parse("https://notfound.com")
		err = RemoveCredential(Uri(*u))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no credentials found")
	})
}

func TestUpdateCreds(t *testing.T) {
	// Save original values and restore after test
	origCredPath := credPath
	origLoadedCredentials := loadedCredentials
	defer func() {
		credPath = origCredPath
		loadedCredentials = origLoadedCredentials
	}()

	// Reset loaded state
	loaded = sync.Once{}

	t.Run("update credentials file", func(t *testing.T) {
		tmpDir := t.TempDir()
		credPath = filepath.Join(tmpDir, "credentials.toml")

		u, _ := url.Parse("https://example.com")

		// First load credentials (this will initialize the sync.Once with empty slice)
		err := LoadCredentials()
		require.NoError(t, err)

		// Now set the credentials
		loadedCredentials = []Credential{
			{
				Type:        TypeApiKey,
				RegistryURL: Uri(*u),
				ApiKey:      "test-key",
			},
		}

		err = UpdateCreds()
		require.NoError(t, err)

		// Verify file was created
		_, err = os.Stat(credPath)
		assert.NoError(t, err)

		// Reset sync.Once to read from file
		loaded = sync.Once{}
		loadedCredentials = nil

		// Verify content
		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Len(t, creds, 1)
		assert.Equal(t, "test-key", creds[0].ApiKey)
	})
}
