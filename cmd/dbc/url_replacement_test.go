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
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// prefixedRegistry serves a registry (index and test archive) below prefix
// and records the requests it receives.
type prefixedRegistry struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
}

func newPrefixedRegistry(index, archive []byte, prefix string) *prefixedRegistry {
	r := &prefixedRegistry{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.requests = append(r.requests, recordedRequest{
			Path: req.URL.Path, Authorization: req.Header.Get("Authorization"), Token: req.URL.RawQuery,
		})
		r.mu.Unlock()
		switch req.URL.Path {
		case prefix + "/index.yaml":
			_, _ = w.Write(index)
		case prefix + "/test-driver-1/1.0.0/test-driver-1.tar.gz":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, req)
		}
	}))
	return r
}

func (r *prefixedRegistry) recorded() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedRequest(nil), r.requests...)
}

func writeUserConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600))
	return dir
}

func parseTestURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func mirrorRuleConfig(prefix, replacement string) string {
	return fmt.Sprintf("[[network.url_replacements]]\nprefix = %q\nreplacement = %q\n", prefix, replacement)
}

// realBase uses the real client for the registry, as the CLI does. The suite
// replaces the getDriverRegistry variable, so the original is used here.
func (suite *SubcommandTestSuite) realBase() baseModel {
	base := defaultBaseModel()
	base.getDriverRegistry = suite.getDriverRegistryFn
	return base
}

// startMirrorFixture starts a canonical registry (which must stay unused) and
// a mirror below /m, configures DBC_BASE_URL to the canonical registry and
// the user config to send it to the mirror.
func (suite *SubcommandTestSuite) startMirrorFixture(withRule bool) (upstream, mirror *prefixedRegistry) {
	t := suite.T()
	archive := readTestArchive(suite, "test-driver-1.tar.gz")
	index := []byte(fmt.Sprintf(`drivers:
  - name: Test Driver 1
    description: This is a test driver
    license: MIT
    path: test-driver-1
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: %s
            url: test-driver-1/1.0.0/test-driver-1.tar.gz
            hash: sha256:%s
`, config.PlatformTuple(), testArchiveSHA256(suite, "test-driver-1.tar.gz")))
	upstream = newPrefixedRegistry(index, archive, "")
	mirror = newPrefixedRegistry(index, archive, "/m")
	t.Cleanup(upstream.Close)
	t.Cleanup(mirror.Close)

	t.Setenv("DBC_BASE_URL", upstream.URL)
	suite.useCredentials(upstream.URL, "REGISTRYTOKEN")
	resetClientState()
	t.Cleanup(resetClientState)
	if withRule {
		suite.applyMirrorRule(mirror, upstream)
	}
	return upstream, mirror
}

func (suite *SubcommandTestSuite) applyMirrorRule(mirror, upstream *prefixedRegistry) {
	resetClientState()
	dir := writeUserConfig(suite.T(), mirrorRuleConfig(upstream.URL, mirror.URL+"/m"))
	suite.Empty(loadStartupRegistryConfig(dir))
	suite.NoError(startupRegistryConfigErr)
}

func (suite *SubcommandTestSuite) TestSyncThroughMirrorKeepsCanonicalLock() {
	upstream, mirror := suite.startMirrorFixture(true)
	listPath := suite.writeDriverList("test-driver-1")

	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(suite.realBase()))

	suite.driverIsInstalledWithVersion("test-driver-1", "1.0.0", true)
	suite.Empty(upstream.recorded(), "the canonical registry must not be contacted")
	got := mirror.recorded()
	suite.Require().NotEmpty(got)
	paths := map[string]bool{}
	for _, r := range got {
		paths[r.Path] = true
		suite.Empty(r.Authorization, "mirror requests are anonymous")
		suite.Empty(r.Token, "mirror requests carry no mid/uid query")
	}
	suite.True(paths["/m/index.yaml"], "index fetched through the mirror")
	suite.True(paths["/m/test-driver-1/1.0.0/test-driver-1.tar.gz"], "archive fetched through the mirror")

	lock, err := os.ReadFile(suite.lockPath())
	suite.Require().NoError(err)
	suite.Contains(string(lock), upstream.URL)
	suite.NotContains(string(lock), mirror.URL)
	suite.NotContains(string(lock), "/m/")
}

func (suite *SubcommandTestSuite) TestInstallThroughMirror() {
	upstream, mirror := suite.startMirrorFixture(true)

	suite.runCmd(InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.GetModelCustom(suite.realBase()))

	suite.driverIsInstalledWithVersion("test-driver-1", "1.0.0", true)
	suite.Empty(upstream.recorded())
	suite.NotEmpty(mirror.recorded())
}

func (suite *SubcommandTestSuite) TestSyncReplaysCanonicalLockThroughMirror() {
	upstream, mirror := suite.startMirrorFixture(false)
	listPath := suite.writeDriverList("test-driver-1")

	// Without a mirror the lock is created against the canonical registry.
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(suite.realBase()))
	suite.NotEmpty(upstream.recorded())
	lockBefore, err := os.ReadFile(suite.lockPath())
	suite.Require().NoError(err)
	upstreamBefore := len(upstream.recorded())

	// A second machine behind the mirror replays the same lock.
	suite.T().Setenv("ADBC_DRIVER_PATH", filepath.Join(suite.tempdir, "second"))
	suite.applyMirrorRule(mirror, upstream)
	replay := suite.realBase()
	replay.getDriverRegistry = func() ([]dbc.Driver, error) { return nil, fmt.Errorf("index must not be fetched") }
	replay.getRegistryIndex = func(dbc.Registry) ([]dbc.Driver, error) { return nil, fmt.Errorf("index must not be fetched") }
	suite.runCmd(SyncCmd{Path: listPath, Level: config.ConfigEnv, Json: true}.GetModelCustom(replay))

	suite.Len(upstream.recorded(), upstreamBefore, "replay must not contact the canonical registry")
	got := mirror.recorded()
	suite.Require().Len(got, 1)
	suite.Equal("/m/test-driver-1/1.0.0/test-driver-1.tar.gz", got[0].Path)
	suite.Empty(got[0].Authorization)
	lockAfter, err := os.ReadFile(suite.lockPath())
	suite.Require().NoError(err)
	suite.Equal(string(lockBefore), string(lockAfter), "the lock stays canonical")
}

func TestInvalidURLReplacementsFailRegistryCommands(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")
	resetClientState()
	t.Cleanup(resetClientState)

	dir := writeUserConfig(t, mirrorRuleConfig("https://dbc-cdn.columnar.tech", "http://mirror.example.com"))
	assert.True(t, strings.HasPrefix(loadStartupRegistryConfig(dir), "warning:"))
	require.Error(t, startupRegistryConfigErr)

	c, err := newDBCClient(nil, nil)
	require.Nil(t, c)
	require.ErrorIs(t, err, dbc.ErrInvalidURLReplacement)
	assert.Contains(t, err.Error(), "url_replacements[0]")

	_, err = fetchRegistryIndex(dbc.Registry{BaseURL: testRegistry.BaseURL})
	require.ErrorIs(t, err, dbc.ErrInvalidURLReplacement)

	_, err = getRegistryViaClient()
	require.ErrorIs(t, err, dbc.ErrInvalidURLReplacement)

	// The download path does not need a client, so it has to fail by itself.
	_, err = downloadPkg(dbc.PkgInfo{Path: testRegistry.BaseURL.JoinPath("x.tar.gz")})
	require.ErrorIs(t, err, dbc.ErrInvalidURLReplacement)

}

func TestGlobalConfigLoadErrorsStopRegistryOperations(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")
	t.Cleanup(resetClientState)
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name: "wrong prefix type",
			config: `[[network.url_replacements]]
prefix = 123
replacement = "https://mirror.example.com"
`,
			wantErr: "prefix",
		},
		{
			name:    "malformed TOML",
			config:  "this is not toml",
			wantErr: "invalid",
		},
		{
			name: "valid mirror with invalid registry",
			config: `[[network.url_replacements]]
prefix = "https://upstream.example.com"
replacement = "https://mirror.example.com"

[[registries]]
url = "not a registry URL"
`,
			wantErr: "registry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetClientState()
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_, _ = io.WriteString(w, "unexpected request")
			}))
			defer server.Close()
			dir := writeUserConfig(t, tt.config)
			assert.True(t, strings.HasPrefix(loadStartupRegistryConfig(dir), "warning:"))
			require.Error(t, startupRegistryConfigErr)
			assert.Contains(t, strings.ToLower(startupRegistryConfigErr.Error()), tt.wantErr)

			_, err := newDBCClient(nil, nil)
			require.ErrorIs(t, err, startupRegistryConfigErr)

			_, err = fetchRegistryIndex(dbc.Registry{BaseURL: parseTestURL(t, server.URL)})
			require.ErrorIs(t, err, startupRegistryConfigErr)

			_, err = downloadPkg(dbc.PkgInfo{Path: parseTestURL(t, server.URL+"/x.tar.gz")})
			require.ErrorIs(t, err, startupRegistryConfigErr)
			assert.Zero(t, hits.Load(), "registry configuration failures stop requests before HTTP")
		})
	}

	t.Run("missing config keeps defaults", func(t *testing.T) {
		resetClientState()
		assert.Empty(t, loadStartupRegistryConfig(t.TempDir()))
		assert.NoError(t, startupRegistryConfigErr)
	})

	t.Run("valid config permits operations", func(t *testing.T) {
		resetClientState()
		dir := writeUserConfig(t, "replace_defaults = false\n")
		assert.Empty(t, loadStartupRegistryConfig(dir))
		assert.NoError(t, startupRegistryConfigErr)
		_, err := newDBCClient(nil, nil)
		require.NoError(t, err)
	})

	t.Run("config read error stops registry operations", func(t *testing.T) {
		resetClientState()
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "config.toml"), 0o700))
		assert.True(t, strings.HasPrefix(loadStartupRegistryConfig(dir), "warning:"))
		require.Error(t, startupRegistryConfigErr)
		_, err := newDBCClient(nil, nil)
		require.ErrorIs(t, err, startupRegistryConfigErr)
	})

	t.Run("help remains available with an invalid config", func(t *testing.T) {
		result := runStartup(writeUserConfig(t, "this is not toml"), []string{"--help"})
		require.Equal(t, startupHelp, result.kind)
	})
}

// getRegistryViaClient runs the real lazy client initialisation.
func getRegistryViaClient() ([]dbc.Driver, error) {
	if err := initDBCClient(); err != nil {
		return nil, err
	}
	return dbcClient.Search(context.Background(), "")
}

func TestProjectNetworkTableIsRejected(t *testing.T) {
	t.Setenv("DBC_BASE_URL", "")
	resetClientState()
	t.Cleanup(resetClientState)

	path := filepath.Join(t.TempDir(), "dbc.toml")
	require.NoError(t, os.WriteFile(path, []byte(`[drivers]
[drivers.test-driver-1]

[[network.url_replacements]]
prefix = "https://dbc-cdn.columnar.tech"
replacement = "https://evil.example.com"
`), 0o600))

	list, err := openAndDecodeDriverList(path)
	require.NoError(t, err)
	require.ErrorIs(t, applyProjectRegistries(list), dbc.ErrProjectNetwork)

	_, err = GetDriverList(path)
	require.ErrorIs(t, err, dbc.ErrProjectNetwork)

	t.Chdir(filepath.Dir(path))
	require.ErrorIs(t, applyProjectRegistriesFromCWD(), dbc.ErrProjectNetwork)
}
