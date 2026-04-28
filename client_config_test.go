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

//go:build !windows

package dbc_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const clientTestManifestTOML = `
name = 'Test Driver'
publisher = 'Test Publisher'
license = 'MIT'
version = '1.2.3'
source = 'dbc'

[ADBC]
version = '1.1.0'

[Driver]
entrypoint = 'AdbcDriverInit'

[Driver.shared]
linux_amd64 = '/path/to/driver.so'
`

func newTestClient(t *testing.T) *dbc.Client {
	t.Helper()
	c, err := dbc.NewClient()
	require.NoError(t, err)
	return c
}

func makeClientTestDriverInfo(id string, filePath string) config.DriverInfo {
	di := config.DriverInfo{
		ID:        id,
		FilePath:  filePath,
		Name:      "Test Driver",
		Publisher: "Test Publisher",
		License:   "MIT",
		Version:   semver.MustParse("1.2.3"),
		Source:    "dbc",
	}
	di.Driver.Entrypoint = "AdbcDriverInit"
	di.Driver.Shared.Set("linux_amd64", filepath.Join(filePath, id, "driver.so"))
	return di
}

func TestClientGetConfig(t *testing.T) {
	t.Run("returns_config_for_env_level", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(tmpDir, "mydriver.toml"),
			[]byte(clientTestManifestTOML),
			0644,
		))
		t.Setenv("ADBC_DRIVER_PATH", tmpDir)

		c := newTestClient(t)
		cfg := c.GetConfig(config.ConfigEnv)

		assert.Equal(t, config.ConfigEnv, cfg.Level)
		assert.True(t, cfg.Exists)
	})

	t.Run("returns_config_for_system_level", func(t *testing.T) {
		c := newTestClient(t)
		cfg := c.GetConfig(config.ConfigSystem)

		assert.Equal(t, config.ConfigSystem, cfg.Level)
	})
}

func TestClientListInstalled(t *testing.T) {
	t.Run("returns_installed_drivers", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("ADBC_DRIVER_PATH", tmpDir)

		require.NoError(t, os.WriteFile(
			filepath.Join(tmpDir, "driver1.toml"),
			[]byte(clientTestManifestTOML),
			0644,
		))
		require.NoError(t, os.WriteFile(
			filepath.Join(tmpDir, "driver2.toml"),
			[]byte(clientTestManifestTOML),
			0644,
		))

		c := newTestClient(t)
		drivers := c.ListInstalled(config.ConfigEnv)
		require.Len(t, drivers, 2)

		ids := make([]string, len(drivers))
		for i, d := range drivers {
			ids[i] = d.ID
		}
		assert.ElementsMatch(t, []string{"driver1", "driver2"}, ids)
	})

	t.Run("empty_path_returns_empty_slice", func(t *testing.T) {
		t.Setenv("ADBC_DRIVER_PATH", "")
		t.Setenv("VIRTUAL_ENV", "")
		t.Setenv("CONDA_PREFIX", "")

		c := newTestClient(t)
		drivers := c.ListInstalled(config.ConfigEnv)
		assert.Empty(t, drivers)
	})
}

func TestClientGetDriver(t *testing.T) {
	t.Run("found_in_env_config", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(tmpDir, "mydriver.toml"),
			[]byte(clientTestManifestTOML),
			0644,
		))

		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: tmpDir,
		}

		c := newTestClient(t)
		di, err := c.GetDriver(cfg, "mydriver")
		require.NoError(t, err)
		assert.Equal(t, "mydriver", di.ID)
		assert.Equal(t, "Test Driver", di.Name)
		assert.Equal(t, "1.2.3", di.Version.String())
	})

	t.Run("not_found_returns_error", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: tmpDir,
		}

		c := newTestClient(t)
		_, err := c.GetDriver(cfg, "nonexistent")
		assert.Error(t, err)
	})
}

func TestClientCreateManifest(t *testing.T) {
	t.Run("creates_manifest_file", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: tmpDir,
		}

		di := makeClientTestDriverInfo("mydriver", tmpDir)

		c := newTestClient(t)
		err := c.CreateManifest(cfg, di)
		require.NoError(t, err)

		assert.FileExists(t, filepath.Join(tmpDir, "mydriver.toml"))
	})

	t.Run("creates_location_if_absent", func(t *testing.T) {
		newDir := filepath.Join(t.TempDir(), "nonexistent", "subdir")
		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: newDir,
		}

		di := makeClientTestDriverInfo("newdriver", newDir)

		c := newTestClient(t)
		err := c.CreateManifest(cfg, di)
		require.NoError(t, err)

		assert.FileExists(t, filepath.Join(newDir, "newdriver.toml"))
	})
}
