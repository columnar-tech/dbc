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

package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/columnar-tech/dbc/internal/fslock"
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
		assert.Equal(t, "existing-token", cred.GetAuthToken(t.Context()))
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

		token := cred.GetAuthToken(t.Context())
		assert.Equal(t, "", token)
	})
}

func TestCredential_Refresh_ApiKey(t *testing.T) {
	t.Run("successful refresh with apikey", func(t *testing.T) {
		// Create test server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
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

		err := cred.Refresh(t.Context())
		assert.NoError(t, err)
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

		err := cred.Refresh(t.Context())
		assert.Error(t, err)
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

		err := cred.Refresh(t.Context())
		assert.Error(t, err)
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
	resetCredState(t)

	t.Run("get existing credentials", func(t *testing.T) {
		credPath = withTempCredPath(t)

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
	ResetCredentialsForTesting()

	t.Run("return nil when credentials not found", func(t *testing.T) {
		credPath = withTempCredPath(t)

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
	resetCredState(t)

	t.Run("add new credential", func(t *testing.T) {
		credPath = withTempCredPath(t)

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
	ResetCredentialsForTesting()

	t.Run("return error when credential already exists", func(t *testing.T) {
		credPath = withTempCredPath(t)

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
	ResetCredentialsForTesting()

	t.Run("overwrite existing credential when allowOverwrite is true", func(t *testing.T) {
		credPath = withTempCredPath(t)

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

func TestAddCredential_UsesLatestDiskSnapshot(t *testing.T) {
	resetCredState(t)
	withTempCredPath(t)

	require.NoError(t, LoadCredentials())
	staleSnapshot := loadedCredentials
	first := Credential{
		Type:        TypeApiKey,
		RegistryURL: Uri(url.URL{Scheme: "https", Host: "first.example.com"}),
		ApiKey:      "first-key",
	}
	second := Credential{
		Type:        TypeApiKey,
		RegistryURL: Uri(url.URL{Scheme: "https", Host: "second.example.com"}),
		ApiKey:      "second-key",
	}

	require.NoError(t, AddCredential(first, false))
	// Simulate another runtime that loaded its snapshot before the first write.
	credMu.Lock()
	loadedCredentials = staleSnapshot
	credMu.Unlock()
	require.NoError(t, AddCredential(second, false))

	creds, err := loadCreds()
	require.NoError(t, err)
	assert.Equal(t, []Credential{first, second}, creds)
	cached, err := GetCredentials((*url.URL)(&first.RegistryURL))
	require.NoError(t, err)
	assert.Equal(t, &first, cached)
}

func TestRemoveCredential(t *testing.T) {
	resetCredState(t)

	t.Run("remove existing credential", func(t *testing.T) {
		credPath = withTempCredPath(t)

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
	ResetCredentialsForTesting()

	t.Run("return error when credential not found", func(t *testing.T) {
		credPath = withTempCredPath(t)

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

func TestRemoveCredential_UsesLatestDiskSnapshot(t *testing.T) {
	resetCredState(t)
	withTempCredPath(t)

	removed := Credential{
		Type:        TypeApiKey,
		RegistryURL: Uri(url.URL{Scheme: "https", Host: "removed.example.com"}),
		ApiKey:      "removed-key",
	}
	retained := Credential{
		Type:        TypeApiKey,
		RegistryURL: Uri(url.URL{Scheme: "https", Host: "retained.example.com"}),
		ApiKey:      "retained-key",
	}
	require.NoError(t, AddCredential(removed, false))
	require.NoError(t, LoadCredentials())
	staleSnapshot := append([]Credential(nil), loadedCredentials...)

	require.NoError(t, AddCredential(retained, false))
	// Simulate another runtime that loaded its snapshot before the latest write.
	credMu.Lock()
	loadedCredentials = staleSnapshot
	credMu.Unlock()
	require.NoError(t, RemoveCredential(removed.RegistryURL))

	creds, err := loadCreds()
	require.NoError(t, err)
	assert.Equal(t, []Credential{retained}, creds)
	cached, err := GetCredentials((*url.URL)(&retained.RegistryURL))
	require.NoError(t, err)
	assert.Equal(t, &retained, cached)
}

// writeCredFile replaces the credentials file without touching this
// process's cache, like another dbc process would.
func writeCredFile(t *testing.T, creds ...Credential) {
	t.Helper()
	f, err := os.Create(credPath)
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, toml.NewEncoder(f).Encode(struct {
		Credentials []Credential `toml:"credentials"`
	}{Credentials: creds}))
}

func TestUpdateCreds(t *testing.T) {
	resetCredState(t)

	t.Run("update credentials file", func(t *testing.T) {
		credPath = withTempCredPath(t)

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

		// Reset to read from file
		ResetCredentialsForTesting()

		// Verify content
		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Len(t, creds, 1)
		assert.Equal(t, "test-key", creds[0].ApiKey)
	})
}

func TestPersistRefreshed(t *testing.T) {
	cred := func(host, token string) Credential {
		return Credential{
			Type:        TypeToken,
			RegistryURL: Uri(url.URL{Scheme: "https", Host: host}),
			Token:       token,
		}
	}

	t.Run("merges a refreshed credential into the latest file", func(t *testing.T) {
		resetCredState(t)
		withTempCredPath(t)
		writeCredFile(t, cred("a.example.com", "old"), cred("gone.example.com", "x"))

		stored, err := GetCredentials(&url.URL{Scheme: "https", Host: "a.example.com"})
		require.NoError(t, err)
		// Another process logs in to b and out of gone after this one loaded.
		writeCredFile(t, cred("a.example.com", "old"), cred("b.example.com", "b-token"))

		before := *stored
		stored.Token = "refreshed"
		require.NoError(t, persistRefreshed(before, stored))

		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Equal(t, []Credential{cred("a.example.com", "refreshed"), cred("b.example.com", "b-token")}, creds)
	})

	t.Run("does not resurrect a credential removed elsewhere", func(t *testing.T) {
		resetCredState(t)
		withTempCredPath(t)
		writeCredFile(t, cred("a.example.com", "old"))

		stored, err := GetCredentials(&url.URL{Scheme: "https", Host: "a.example.com"})
		require.NoError(t, err)
		writeCredFile(t)

		before := *stored
		stored.Token = "refreshed"
		require.NoError(t, persistRefreshed(before, stored))

		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Empty(t, creds)
	})

	t.Run("does not persist a credential that is not from the store", func(t *testing.T) {
		resetCredState(t)
		withTempCredPath(t)
		writeCredFile(t, cred("a.example.com", "stored"))
		require.NoError(t, LoadCredentials())
		writeCredFile(t, cred("a.example.com", "stored"), cred("b.example.com", "b-token"))

		// An injected credential for a stored host must not replace it, and
		// must not rewrite the file from this process's stale cache.
		injected := cred("a.example.com", "injected")
		before := injected
		injected.Token = "refreshed"
		require.NoError(t, persistRefreshed(before, &injected))

		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Equal(t, []Credential{cred("a.example.com", "stored"), cred("b.example.com", "b-token")}, creds)
	})

	t.Run("an injected credential never loads the store", func(t *testing.T) {
		resetCredState(t)
		withTempCredPath(t)
		require.NoError(t, os.WriteFile(credPath, []byte("not = [valid toml"), 0o600))

		injected := cred("a.example.com", "injected")
		before := injected
		injected.Token = "refreshed"
		require.NoError(t, persistRefreshed(before, &injected))

		// The broken file must not have been cached as this process's store.
		writeCredFile(t, cred("a.example.com", "stored"))
		require.NoError(t, LoadCredentials())
	})

	t.Run("keeps persisting after a write replaces the cache", func(t *testing.T) {
		resetCredState(t)
		withTempCredPath(t)
		writeCredFile(t, cred("a.example.com", "old"))

		stored, err := GetCredentials(&url.URL{Scheme: "https", Host: "a.example.com"})
		require.NoError(t, err)
		// e.g. GetAuthToken refreshes an empty token, then the request gets a
		// 401 and refreshes the same credential again.
		for _, token := range []string{"first", "second"} {
			before := *stored
			stored.Token = token
			require.NoError(t, persistRefreshed(before, stored))
		}

		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Equal(t, []Credential{cred("a.example.com", "second")}, creds)
	})

	t.Run("keeps a newer login for the same host", func(t *testing.T) {
		resetCredState(t)
		withTempCredPath(t)
		writeCredFile(t, cred("a.example.com", "old"))

		stored, err := GetCredentials(&url.URL{Scheme: "https", Host: "a.example.com"})
		require.NoError(t, err)
		// Another process logs in to the same host again.
		writeCredFile(t, cred("a.example.com", "relogin"))

		before := *stored
		stored.Token = "refreshed"
		require.NoError(t, persistRefreshed(before, stored))

		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Equal(t, []Credential{cred("a.example.com", "relogin")}, creds)
	})
}

// Writers serialize on the credentials file lock, which another dbc process
// would hold during its own read-modify-write.
func TestCredentialWritersWaitForFileLock(t *testing.T) {
	resetCredState(t)
	withTempCredPath(t)
	cred := Credential{
		Type:        TypeToken,
		RegistryURL: Uri(url.URL{Scheme: "https", Host: "a.example.com"}),
		Token:       "token",
	}

	t.Run("waits for the holder", func(t *testing.T) {
		held, err := fslock.Acquire(credPath+".lock", 0)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { done <- AddCredential(cred, true) }()
		select {
		case err := <-done:
			t.Fatalf("AddCredential returned while the lock was held: %v", err)
		case <-time.After(200 * time.Millisecond):
		}
		require.NoError(t, held.Release())
		require.NoError(t, <-done)
		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Equal(t, []Credential{cred}, creds)
	})

	t.Run("fails when the holder does not release", func(t *testing.T) {
		orig := credentialLockTimeout
		credentialLockTimeout = 50 * time.Millisecond
		t.Cleanup(func() { credentialLockTimeout = orig })
		held, err := fslock.Acquire(credPath+".lock", 0)
		require.NoError(t, err)
		t.Cleanup(func() { _ = held.Release() })

		err = RemoveCredential(cred.RegistryURL)
		require.ErrorContains(t, err, "failed to lock credentials file")
		creds, err := loadCreds()
		require.NoError(t, err)
		assert.Equal(t, []Credential{cred}, creds)
	})
}

// Readers in other processes parse the store without the writers' lock, so a
// write must never be observable half done.
func TestCredentialWritesAreNeverObservedPartial(t *testing.T) {
	resetCredState(t)
	withTempCredPath(t)
	u, err := url.Parse("https://registry.example.test")
	require.NoError(t, err)
	cred := func(i int) Credential {
		// Alternate sizes so a truncated or partly written file can't parse.
		return Credential{Type: TypeToken, RegistryURL: Uri(*u), AuthURI: Uri(*u), Token: strings.Repeat("t", 1+(i%2)*64*1024)}
	}
	require.NoError(t, AddCredential(cred(0), true))

	done := make(chan error, 1)
	go func() {
		for i := 1; i <= 200; i++ {
			if err := AddCredential(cred(i), true); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for {
		select {
		case err := <-done:
			require.NoError(t, err)
			return
		default:
		}
		// loadCreds takes no lock, like LoadCredentials in another process.
		creds, err := loadCreds()
		require.NoError(t, err, "a reader observed a partially written credentials file")
		require.Len(t, creds, 1)
	}
}

// Without force, an existing license is never overwritten, even by a racing
// install: exactly one of several concurrent installs may succeed.
func TestInstallLicenseWithoutForceNeverOverwrites(t *testing.T) {
	resetCredState(t)
	withTempCredPath(t)
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		src := filepath.Join(t.TempDir(), "columnar.lic")
		require.NoError(t, os.WriteFile(src, []byte(fmt.Sprintf("license-%d", i)), 0o600))
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = InstallLicenseFromFile(src, false)
		}()
	}
	wg.Wait()

	var winners []int
	for i, err := range errs {
		if err == nil {
			winners = append(winners, i)
		} else {
			require.ErrorIs(t, err, ErrLicenseAlreadyExists)
		}
	}
	require.Len(t, winners, 1, "exactly one non-force install may succeed")
	dest, err := LicensePath()
	require.NoError(t, err)
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("license-%d", winners[0]), string(got))
}
