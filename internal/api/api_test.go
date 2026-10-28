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

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixtureServer(t *testing.T) string {
	t.Helper()
	testdata := filepath.Join("..", "..", "cmd", "dbc", "testdata")
	index, err := os.ReadFile(filepath.Join(testdata, "test_index.yaml"))
	require.NoError(t, err)
	tarball, err := os.ReadFile(filepath.Join(testdata, "test-driver-1.tar.gz"))
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/index.yaml":
			w.Write(index)
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func newClient(t *testing.T) *dbc.Client {
	t.Helper()
	c, err := api.NewClient(api.ClientConfig{BaseURL: fixtureServer(t)}, &http.Client{})
	require.NoError(t, err)
	return c
}

func TestNewClientRejectsInvalidCredentialURL(t *testing.T) {
	_, err := api.NewClient(api.ClientConfig{Credential: &api.Credential{
		RegistryURL: "http://\x00",
		AuthURI:     "http://example.test",
		Token:       "t",
	}}, nil)
	require.ErrorContains(t, err, "invalid credential registryURL")
}

// The JSON names are consumed by @columnar-tech/dbc-wasm and the columnar-dbc
// crate; a renamed tag compiles fine in Go and silently breaks both.
func TestSearchWireFormat(t *testing.T) {
	res, err := api.Search(t.Context(), newClient(t), "test-driver-1")
	require.NoError(t, err)

	b, err := json.Marshal(res)
	require.NoError(t, err)
	var wire struct {
		Drivers []map[string]any `json:"drivers"`
	}
	require.NoError(t, json.Unmarshal(b, &wire))
	require.NotEmpty(t, wire.Drivers)
	assert.Equal(t, "test-driver-1", wire.Drivers[0]["path"])
	for _, key := range []string{"title", "license", "description"} {
		assert.Contains(t, wire.Drivers[0], key)
	}
	assert.NotContains(t, string(b), `"warning"`, "warning must be omitted when every registry answered")
}

func TestResolve(t *testing.T) {
	c := newClient(t)

	t.Run("explicit platform", func(t *testing.T) {
		res, err := api.Resolve(t.Context(), c, "test-driver-1", "linux_amd64")
		require.NoError(t, err)
		assert.Equal(t, "test-driver-1", res.Path)
		assert.Equal(t, "linux_amd64", res.Platform)
		assert.NotEmpty(t, res.Versions)
		require.NotNil(t, res.Latest)
		assert.True(t, strings.HasSuffix(res.Latest.URL, ".tar.gz"), res.Latest.URL)
	})

	t.Run("unknown driver", func(t *testing.T) {
		_, err := api.Resolve(t.Context(), c, "nonexistent-driver", "linux_amd64")
		require.EqualError(t, err, `driver "nonexistent-driver" not found`)
	})
}

func TestInstallListUninstall(t *testing.T) {
	c := newClient(t)
	target := api.Target{Location: t.TempDir()}

	m, err := api.Install(t.Context(), c, "test-driver-1", target, api.InstallOptions{})
	require.NoError(t, err)
	assert.Equal(t, "test-driver-1", m.ID)
	assert.FileExists(t, m.DriverPath)

	listed, err := api.ListInstalled(target)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "test-driver-1", listed[0].ID)

	require.NoError(t, api.Uninstall("test-driver-1", target))
	listed, err = api.ListInstalled(target)
	require.NoError(t, err)
	assert.Empty(t, listed)
}

// Resolution only: writing to the real levels here would race with the CLI's
// level suites, which RemoveAll those directories (see the Decisions table).
func TestTarget(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		cfg, err := api.Target{Location: dir}.Config()
		require.NoError(t, err)
		assert.Equal(t, config.ConfigEnv, cfg.Level)
		assert.Equal(t, dir, cfg.Location)
	})

	t.Run("levels resolve to the CLI's locations", func(t *testing.T) {
		for _, lvl := range []config.ConfigLevel{config.ConfigUser, config.ConfigSystem} {
			cfg, err := api.Target{Level: lvl.String()}.Config()
			require.NoError(t, err, lvl)
			assert.Equal(t, lvl, cfg.Level)
			assert.Equal(t, lvl.ConfigLocation(), cfg.Location)
		}
	})

	for _, tc := range []struct {
		name   string
		target api.Target
		want   string
	}{
		{"both", api.Target{Level: "user", Location: "/tmp/x"}, "set either level or location, not both"},
		{"neither", api.Target{}, "either level or location is required"},
		{"unknown level", api.Target{Level: "env"}, `unknown config level "env", valid values are: user, system`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.target.Config()
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestClientsHandles(t *testing.T) {
	var reg api.Clients
	c := newClient(t)

	h := reg.Add(c)
	got, err := reg.Get(h)
	require.NoError(t, err)
	assert.Same(t, c, got)

	reg.Remove(h)
	_, err = reg.Get(h)
	require.EqualError(t, err, fmt.Sprintf("invalid client handle %d", h))
	assert.NotEqual(t, h, reg.Add(c), "handles must never be reused")
}
