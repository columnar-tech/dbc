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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClientForServer(t *testing.T, serverURL string) *dbc.Client {
	t.Helper()
	c, err := dbc.NewClient(
		dbc.WithHTTPClient(&http.Client{}),
		dbc.WithBaseURL(serverURL),
	)
	require.NoError(t, err)
	return c
}

func newInstallTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	indexData, err := os.ReadFile(filepath.Join("cmd", "dbc", "testdata", "test_index.yaml"))
	require.NoError(t, err)

	tarballData, err := os.ReadFile(filepath.Join("cmd", "dbc", "testdata", "test-driver-1.tar.gz"))
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/index.yaml":
			w.Header().Set("Content-Type", "application/yaml")
			w.Write(indexData)
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Length", fmt.Sprint(len(tarballData)))
			w.Write(tarballData)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClientSearch(t *testing.T) {
	c, err := dbc.NewClient(
		dbc.WithHTTPClient(&http.Client{Transport: &testTransport{}}),
		dbc.WithBaseURL(testServer.URL),
	)
	require.NoError(t, err)

	t.Run("empty pattern returns all drivers", func(t *testing.T) {
		drivers, err := c.Search("")
		require.NoError(t, err)
		assert.NotEmpty(t, drivers)

		var paths []string
		for _, d := range drivers {
			paths = append(paths, d.Path)
		}
		assert.Contains(t, paths, "test-driver-1")
		assert.Contains(t, paths, "test-driver-2")
	})

	t.Run("pattern matches by path", func(t *testing.T) {
		drivers, err := c.Search("test-driver-1")
		require.NoError(t, err)
		require.NotEmpty(t, drivers)

		var paths []string
		for _, d := range drivers {
			paths = append(paths, d.Path)
		}
		assert.Contains(t, paths, "test-driver-1")
		assert.NotContains(t, paths, "test-driver-2")
	})

	t.Run("nonexistent pattern returns empty list", func(t *testing.T) {
		drivers, err := c.Search("nonexistent-driver-xyz")
		require.NoError(t, err)
		assert.Empty(t, drivers)
	})
}

func TestClientInstall(t *testing.T) {
	srv := newInstallTestServer(t)
	c := newTestClientForServer(t, srv.URL)

	tmpDir := t.TempDir()
	cfg := config.Config{
		Level:    config.ConfigEnv,
		Location: tmpDir,
	}

	t.Run("installs driver successfully", func(t *testing.T) {
		manifest, err := c.Install(cfg, "test-driver-1")
		require.NoError(t, err)
		require.NotNil(t, manifest)
		assert.Equal(t, "test-driver-1", manifest.DriverInfo.ID)
		assert.NotNil(t, manifest.DriverInfo.Version)
	})

	t.Run("returns error for nonexistent driver", func(t *testing.T) {
		_, err := c.Install(cfg, "nonexistent-driver")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "nonexistent-driver")
	})
}

func TestClientUninstall(t *testing.T) {
	srv := newInstallTestServer(t)
	c := newTestClientForServer(t, srv.URL)

	t.Run("uninstalls a previously installed driver", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: tmpDir,
		}

		_, err := c.Install(cfg, "test-driver-1")
		require.NoError(t, err)

		manifestPath := filepath.Join(tmpDir, "test-driver-1.toml")
		_, err = os.Stat(manifestPath)
		require.NoError(t, err, "manifest TOML should exist after install")

		err = c.Uninstall(cfg, "test-driver-1")
		require.NoError(t, err)

		_, err = os.Stat(manifestPath)
		assert.True(t, os.IsNotExist(err), "manifest TOML should be removed after uninstall")
	})

	t.Run("returns error for driver not installed", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: tmpDir,
		}
		err := c.Uninstall(cfg, "not-installed-driver")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not-installed-driver")
	})
}
