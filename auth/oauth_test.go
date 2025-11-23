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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOpenIDConfig(t *testing.T) {
	t.Run("successful fetch with oauth-authorization-server endpoint", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{
					"issuer": "https://example.com",
					"authorization_endpoint": "https://example.com/authorize",
					"token_endpoint": "https://example.com/token",
					"device_authorization_endpoint": "https://example.com/device",
					"userinfo_endpoint": "https://example.com/userinfo",
					"jwks_uri": "https://example.com/jwks",
					"scopes_supported": ["openid", "profile"],
					"response_types_supported": ["code"],
					"subject_types_supported": ["public"],
					"id_token_signing_alg_values_supported": ["RS256"],
					"claims_supported": ["sub", "name"],
					"token_endpoint_auth_methods_supported": ["client_secret_basic"],
					"end_session_endpoint": "https://example.com/logout",
					"request_uri_parameter_supported": true,
					"request_parameter_supported": false
				}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		issuerURL, _ := url.Parse(server.URL)
		config, err := GetOpenIDConfig(issuerURL)
		require.NoError(t, err)

		assert.Equal(t, "https://example.com", config.Issuer.String())
		assert.Equal(t, "https://example.com/authorize", config.AuthorizationEndpoint.String())
		assert.Equal(t, "https://example.com/token", config.TokenEndpoint.String())
		assert.Equal(t, "https://example.com/device", config.DeviceAuthorizationEndpoint.String())
		assert.Equal(t, "https://example.com/userinfo", config.UserinfoEndpoint.String())
		assert.Equal(t, "https://example.com/jwks", config.JwksURI.String())
		assert.Equal(t, []string{"openid", "profile"}, config.ScopesSupported)
		assert.Equal(t, []string{"code"}, config.ResponseTypesSupported)
		assert.Equal(t, []string{"public"}, config.SubjectTypesSupported)
		assert.Equal(t, []string{"RS256"}, config.IDTokenSigningAlgValuesSupported)
		assert.Equal(t, []string{"sub", "name"}, config.ClaimsSupported)
		assert.Equal(t, []string{"client_secret_basic"}, config.TokenEndpointAuthMethodsSupported)
		assert.Equal(t, "https://example.com/logout", config.EndSessionEndpoint.String())
		assert.True(t, config.RequestURIParameterSupported)
		assert.False(t, config.RequestParameterSupported)
	})

	t.Run("successful fetch with openid-configuration endpoint", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/openid-configuration" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{
					"issuer": "https://example.com",
					"authorization_endpoint": "https://example.com/authorize",
					"token_endpoint": "https://example.com/token"
				}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		issuerURL, _ := url.Parse(server.URL)
		config, err := GetOpenIDConfig(issuerURL)
		require.NoError(t, err)

		assert.Equal(t, "https://example.com", config.Issuer.String())
		assert.Equal(t, "https://example.com/authorize", config.AuthorizationEndpoint.String())
		assert.Equal(t, "https://example.com/token", config.TokenEndpoint.String())
	})

	t.Run("return error when both endpoints fail", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		issuerURL, _ := url.Parse(server.URL)
		_, err := GetOpenIDConfig(issuerURL)
		assert.Error(t, err)
	})

	t.Run("return error when response is invalid json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`invalid json`))
		}))
		defer server.Close()

		issuerURL, _ := url.Parse(server.URL)
		_, err := GetOpenIDConfig(issuerURL)
		assert.Error(t, err)
	})
}

func TestRefreshOauth(t *testing.T) {
	t.Run("successful token refresh", func(t *testing.T) {
		// Create mock server for OpenID config
		configServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				tokenURL := "http://token-server.local/token"
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{
					"issuer": "https://example.com",
					"token_endpoint": "` + tokenURL + `"
				}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer configServer.Close()

		// Create mock token server
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))

			err := r.ParseForm()
			require.NoError(t, err)

			assert.Equal(t, "refresh_token", r.FormValue("grant_type"))
			assert.Equal(t, "test-client-id", r.FormValue("client_id"))
			assert.Equal(t, "test-refresh-token", r.FormValue("refresh_token"))

			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"access_token": "new-access-token"}`))
		}))
		defer tokenServer.Close()

		// Update the config server to return the token server URL
		tokenServerURL, _ := url.Parse(tokenServer.URL)

		// Create a new config server that returns the actual token server URL
		finalConfigServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{
					"issuer": "https://example.com",
					"token_endpoint": "` + tokenServerURL.String() + `"
				}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer finalConfigServer.Close()

		authURL, _ := url.Parse(finalConfigServer.URL)
		cred := &Credential{
			AuthURI:      Uri(*authURL),
			ClientID:     "test-client-id",
			RefreshToken: "test-refresh-token",
		}

		err := refreshOauth(cred)
		require.NoError(t, err)
		assert.Equal(t, "new-access-token", cred.Token)
	})

	t.Run("return error when openid config fetch fails", func(t *testing.T) {
		u, _ := url.Parse("http://invalid-host-xyz-123.local")
		cred := &Credential{
			AuthURI:      Uri(*u),
			ClientID:     "test-client-id",
			RefreshToken: "test-refresh-token",
		}

		err := refreshOauth(cred)
		assert.Error(t, err)
	})

	t.Run("return error when token endpoint returns error", func(t *testing.T) {
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "unauthorized"}`))
		}))
		defer tokenServer.Close()

		finalConfigServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				tokenServerURL, _ := url.Parse(tokenServer.URL)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{
					"issuer": "https://example.com",
					"token_endpoint": "` + tokenServerURL.String() + `"
				}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer finalConfigServer.Close()

		authURL, _ := url.Parse(finalConfigServer.URL)
		cred := &Credential{
			AuthURI:      Uri(*authURL),
			ClientID:     "test-client-id",
			RefreshToken: "test-refresh-token",
		}

		err := refreshOauth(cred)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "token endpoint returned status")
	})

	t.Run("return error when token response is invalid json", func(t *testing.T) {
		configServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				tokenURL := "http://token-server.local/token"
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{
					"issuer": "https://example.com",
					"token_endpoint": "` + tokenURL + `"
				}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer configServer.Close()

		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`invalid json`))
		}))
		defer tokenServer.Close()

		finalConfigServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				tokenServerURL, _ := url.Parse(tokenServer.URL)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{
					"issuer": "https://example.com",
					"token_endpoint": "` + tokenServerURL.String() + `"
				}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer finalConfigServer.Close()

		authURL, _ := url.Parse(finalConfigServer.URL)
		cred := &Credential{
			AuthURI:      Uri(*authURL),
			ClientID:     "test-client-id",
			RefreshToken: "test-refresh-token",
		}

		err := refreshOauth(cred)
		assert.Error(t, err)
	})
}

func TestCredential_Refresh_OAuth(t *testing.T) {
	t.Run("successful oauth refresh", func(t *testing.T) {
		// Create mock servers
		tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"access_token": "refreshed-token"}`))
		}))
		defer tokenServer.Close()

		configServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				tokenServerURL, _ := url.Parse(tokenServer.URL)
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{
					"issuer": "https://example.com",
					"token_endpoint": "` + tokenServerURL.String() + `"
				}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer configServer.Close()

		authURL, _ := url.Parse(configServer.URL)
		cred := &Credential{
			Type:         TypeToken,
			AuthURI:      Uri(*authURL),
			ClientID:     "test-client-id",
			RefreshToken: "test-refresh-token",
		}

		success := cred.Refresh()
		assert.True(t, success)
		assert.Equal(t, "refreshed-token", cred.Token)
	})

	t.Run("failed oauth refresh", func(t *testing.T) {
		u, _ := url.Parse("http://invalid-host-xyz-123.local")
		cred := &Credential{
			Type:         TypeToken,
			AuthURI:      Uri(*u),
			ClientID:     "test-client-id",
			RefreshToken: "test-refresh-token",
		}

		success := cred.Refresh()
		assert.False(t, success)
	})
}

func TestFetch(t *testing.T) {
	t.Run("successful fetch", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"key": "value"}`))
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		var result map[string]string
		err := fetch(serverURL, &result)
		require.NoError(t, err)
		assert.Equal(t, "value", result["key"])
	})

	t.Run("return error on non-200 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		var result map[string]string
		err := fetch(serverURL, &result)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch")
	})

	t.Run("return error on invalid json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`invalid json`))
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		var result map[string]string
		err := fetch(serverURL, &result)
		assert.Error(t, err)
	})

	t.Run("return error on connection failure", func(t *testing.T) {
		u, _ := url.Parse("http://invalid-host-xyz-123.local")
		var result map[string]string
		err := fetch(u, &result)
		assert.Error(t, err)
	})
}
