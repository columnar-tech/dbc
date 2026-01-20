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

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"

	"github.com/columnar-tech/dbc/auth"
	"github.com/pelletier/go-toml/v2"
)

func (suite *SubcommandTestSuite) TestLoginCmdDefaults() {
	// Test that LoginCmd properly sets defaults
	cmd := LoginCmd{}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	loginM, ok := m.(loginModel)
	suite.Require().True(ok, "expected loginModel")
	suite.Equal(auth.DefaultOauthURI, loginM.inputURI)
	suite.Equal(auth.DefaultOauthClientID, loginM.oauthClientID)
	suite.Equal("", loginM.apiKey)
}

func (suite *SubcommandTestSuite) TestLoginCmdWithRegistryURL() {
	// Test that LoginCmd uses provided registry URL
	cmd := LoginCmd{RegistryURL: "https://custom-registry.example.com"}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	loginM, ok := m.(loginModel)
	suite.Require().True(ok, "expected loginModel")
	suite.Equal("https://custom-registry.example.com", loginM.inputURI)
	suite.Equal("", loginM.oauthClientID)
}

func (suite *SubcommandTestSuite) TestLoginCmdWithClientID() {
	// Test that LoginCmd uses provided client ID when using default registry
	cmd := LoginCmd{ClientID: "custom-client-id"}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	loginM, ok := m.(loginModel)
	suite.Require().True(ok, "expected loginModel")
	suite.Equal(auth.DefaultOauthURI, loginM.inputURI)
	suite.Equal("custom-client-id", loginM.oauthClientID)
}

func (suite *SubcommandTestSuite) TestLoginCmdWithApiKey() {
	// Setup temp credential path
	tmpDir := suite.T().TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")
	restore := auth.SetCredPathForTesting(credPath)
	defer restore()
	auth.ResetCredentialsForTesting()

	// Create TLS test server that responds to API key authentication
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The login path is automatically appended by the code
		if r.URL.Path == "/login" {
			authHeader := r.Header.Get("Authorization")
			if authHeader != "Bearer test-api-key" {
				suite.T().Logf("Unexpected auth header: %s", authHeader)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"access_token": "test-token",
			})
		} else {
			suite.T().Logf("Unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Use the TLS test server's client to avoid certificate errors
	// We need to inject this into the HTTP client used by auth.Credential.Refresh()
	// For now, let's just test that the command properly sets up the model
	// and skip the actual HTTP call verification

	cmd := LoginCmd{
		RegistryURL: server.URL,
		ApiKey:      "test-api-key",
	}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	// Verify the model was created with correct parameters
	loginM, ok := m.(loginModel)
	suite.Require().True(ok, "expected loginModel")
	suite.Equal(server.URL, loginM.inputURI)
	suite.Equal("test-api-key", loginM.apiKey)

	// Note: We can't easily test the full flow without mocking the HTTP client
	// used by auth.Credential.Refresh(), so we'll just verify the setup is correct
}

func (suite *SubcommandTestSuite) TestLoginCmdInvalidURL() {
	// Test LoginCmd with invalid URL - Go's url.Parse is quite permissive,
	// so we expect the error to occur during OpenID config fetch
	cmd := LoginCmd{RegistryURL: "ht!tp://invalid url"}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	out := suite.runCmdErr(m)
	// The error will occur when trying to fetch OpenID config
	suite.Contains(out, "Error:")
}

func (suite *SubcommandTestSuite) TestLoginCmdApiKeyAuthFails() {
	// Setup temp credential path
	tmpDir := suite.T().TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")
	restore := auth.SetCredPathForTesting(credPath)
	defer restore()
	auth.ResetCredentialsForTesting()

	// Create test server that rejects API key
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Test LoginCmd with invalid API key
	cmd := LoginCmd{
		RegistryURL: server.URL,
		ApiKey:      "invalid-key",
	}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	out := suite.runCmdErr(m)
	suite.Contains(out, "failed to obtain access token")
}

func (suite *SubcommandTestSuite) TestLogoutCmdDefaults() {
	// Test that LogoutCmd properly sets defaults
	cmd := LogoutCmd{}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	logoutM, ok := m.(logoutModel)
	suite.Require().True(ok, "expected logoutModel")
	suite.Equal(auth.DefaultOauthURI, logoutM.inputURI)
}

func (suite *SubcommandTestSuite) TestLogoutCmdWithRegistryURL() {
	// Test that LogoutCmd uses provided registry URL
	cmd := LogoutCmd{RegistryURL: "https://custom-registry.example.com"}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	logoutM, ok := m.(logoutModel)
	suite.Require().True(ok, "expected logoutModel")
	suite.Equal("https://custom-registry.example.com", logoutM.inputURI)
}

func (suite *SubcommandTestSuite) TestLogoutCmdSuccess() {
	// Setup temp credential path
	tmpDir := suite.T().TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")
	restore := auth.SetCredPathForTesting(credPath)
	defer restore()
	auth.ResetCredentialsForTesting()

	// Add a credential
	u, _ := url.Parse("https://example.com")
	cred := auth.Credential{
		Type:        auth.TypeApiKey,
		RegistryURL: auth.Uri(*u),
		ApiKey:      "test-key",
	}
	err := auth.AddCredential(cred, false)
	suite.Require().NoError(err)

	// Verify credential exists
	storedCred, err := auth.GetCredentials(u)
	suite.Require().NoError(err)
	suite.Require().NotNil(storedCred)

	// Test LogoutCmd
	cmd := LogoutCmd{RegistryURL: "https://example.com"}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	suite.runCmd(m)

	// Verify credential was removed
	auth.ResetCredentialsForTesting()
	storedCred, err = auth.GetCredentials(u)
	suite.Require().NoError(err)
	suite.Nil(storedCred)
}

func (suite *SubcommandTestSuite) TestLogoutCmdPurge() {
	// Setup temp credential path
	tmpDir := suite.T().TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")
	restore := auth.SetCredPathForTesting(credPath)
	defer restore()
	auth.ResetCredentialsForTesting()

	// Add a credential
	u, _ := url.Parse("https://example.com")
	cred := auth.Credential{
		Type:        auth.TypeApiKey,
		RegistryURL: auth.Uri(*u),
		ApiKey:      "test-key",
	}
	err := auth.AddCredential(cred, false)
	suite.Require().NoError(err)

	// Verify credential exists
	storedCred, err := auth.GetCredentials(u)
	suite.Require().NoError(err)
	suite.Require().NotNil(storedCred)

	// Test LogoutCmd with purge
	cmd := LogoutCmd{
		RegistryURL: "https://example.com",
		Purge:       true,
	}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	licPath := filepath.Join(tmpDir, "columnar.lic")
	f, err := os.Create(licPath)
	suite.Require().NoError(err)
	f.Close()

	suite.runCmd(m)

	// Verify credentials file was removed
	suite.NoFileExists(credPath)
	suite.NoFileExists(licPath)
}

func (suite *SubcommandTestSuite) TestLogoutCmdNotFound() {
	// Setup temp credential path
	tmpDir := suite.T().TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")
	restore := auth.SetCredPathForTesting(credPath)
	defer restore()
	auth.ResetCredentialsForTesting()

	// Ensure credentials file exists but is empty
	suite.Require().NoError(os.MkdirAll(filepath.Dir(credPath), 0o700))
	f, err := os.Create(credPath)
	suite.Require().NoError(err)
	suite.Require().NoError(toml.NewEncoder(f).Encode(struct {
		Credentials []auth.Credential `toml:"credentials"`
	}{Credentials: []auth.Credential{}}))
	f.Close()

	// Test LogoutCmd with non-existent credential
	cmd := LogoutCmd{RegistryURL: "https://nonexistent.example.com"}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	out := suite.runCmdErr(m)
	suite.Contains(out, "failed to log out")
}

func (suite *SubcommandTestSuite) TestLogoutCmdInvalidURL() {
	// Test LogoutCmd with invalid URL - Go's url.Parse is quite permissive,
	// so we expect the error to occur during credential removal
	cmd := LogoutCmd{RegistryURL: "ht!tp://invalid url"}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	out := suite.runCmdErr(m)
	// The error will occur when trying to remove credentials
	suite.Contains(out, "Error:")
}
