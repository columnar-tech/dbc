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

package config_test

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testManifestTOML = `
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

func makeTestDriverInfo(id string, filePath string) config.DriverInfo {
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

func TestGet(t *testing.T) {
	t.Run("returns_all_levels", func(t *testing.T) {
		configs := config.Get()
		require.NotNil(t, configs)
		assert.Contains(t, configs, config.ConfigSystem)
		assert.Contains(t, configs, config.ConfigEnv)
	})

	t.Run("env_config_from_adbc_driver_path", func(t *testing.T) {
		tmpDir := t.TempDir()
		manifestPath := filepath.Join(tmpDir, "mydriver.toml")
		require.NoError(t, os.WriteFile(manifestPath, []byte(testManifestTOML), 0644))

		t.Setenv("ADBC_DRIVER_PATH", tmpDir)

		configs := config.Get()
		envCfg := configs[config.ConfigEnv]
		assert.True(t, envCfg.Exists)
		assert.Contains(t, envCfg.Drivers, "mydriver")
	})

	t.Run("empty_env_path_yields_empty_config", func(t *testing.T) {
		t.Setenv("ADBC_DRIVER_PATH", "")
		t.Setenv("VIRTUAL_ENV", "")
		t.Setenv("CONDA_PREFIX", "")

		configs := config.Get()
		envCfg := configs[config.ConfigEnv]
		assert.Equal(t, "", envCfg.Location)
		assert.False(t, envCfg.Exists)
	})
}

func TestInflateTarball(t *testing.T) {
	t.Run("valid_tarball", func(t *testing.T) {
		f, err := os.Open(filepath.Join("..", "cmd", "dbc", "testdata", "test-driver-1.tar.gz"))
		require.NoError(t, err)

		outDir := t.TempDir()
		manifest, err := config.InflateTarball(f, outDir)
		require.NoError(t, err)

		assert.Equal(t, "Test Driver 1", manifest.Name)
		assert.Equal(t, "1.0.0", manifest.Version.String())
		assert.NotEmpty(t, manifest.Files.Driver)
		assert.FileExists(t, filepath.Join(outDir, manifest.Files.Driver))
		assert.FileExists(t, filepath.Join(outDir, manifest.Files.Signature))
	})

	t.Run("invalid_gzip", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "bad-*.tar.gz")
		require.NoError(t, err)
		_, _ = f.Write([]byte("this is not valid gzip data"))
		_, _ = f.Seek(0, io.SeekStart)

		_, err = config.InflateTarball(f, t.TempDir())
		assert.Error(t, err)
	})

	t.Run("directory_entry_rejected", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "dir-*.tar.gz")
		require.NoError(t, err)

		gw := gzip.NewWriter(f)
		tw := tar.NewWriter(gw)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "subdir/",
			Typeflag: tar.TypeDir,
			Mode:     0755,
		}))
		require.NoError(t, tw.Close())
		require.NoError(t, gw.Close())
		_, _ = f.Seek(0, io.SeekStart)

		_, err = config.InflateTarball(f, t.TempDir())
		assert.ErrorContains(t, err, "directory entry")
	})
}

func TestInstallDriver(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("ADBC_DRIVER_PATH", tmpDir)

		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: tmpDir,
		}

		f, err := os.Open(filepath.Join("..", "cmd", "dbc", "testdata", "test-driver-1.tar.gz"))
		require.NoError(t, err)

		manifest, err := config.InstallDriver(cfg, "test-driver-1", f)
		require.NoError(t, err)

		assert.Equal(t, "test-driver-1", manifest.DriverInfo.ID)
		assert.Equal(t, "dbc", manifest.DriverInfo.Source)
		assert.Equal(t, "Test Driver 1", manifest.DriverInfo.Name)
		assert.Equal(t, "1.0.0", manifest.DriverInfo.Version.String())

		platformTuple := config.PlatformTuple()
		sharedPath := manifest.DriverInfo.Driver.Shared.Get(platformTuple)
		assert.NotEmpty(t, sharedPath)
		assert.FileExists(t, sharedPath)
	})

	t.Run("invalid_tarball", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: tmpDir,
		}

		f, err := os.CreateTemp(t.TempDir(), "bad-*.tar.gz")
		require.NoError(t, err)
		_, _ = f.Write([]byte("not a tarball"))
		_, _ = f.Seek(0, io.SeekStart)

		_, err = config.InstallDriver(cfg, "bad-driver", f)
		assert.Error(t, err)
	})
}

func TestGetDriver(t *testing.T) {
	t.Run("found_in_env_config", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(tmpDir, "mydriver.toml"),
			[]byte(testManifestTOML),
			0644,
		))

		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: tmpDir,
		}

		di, err := config.GetDriver(cfg, "mydriver")
		require.NoError(t, err)
		assert.Equal(t, "mydriver", di.ID)
		assert.Equal(t, "Test Driver", di.Name)
		assert.Equal(t, "1.2.3", di.Version.String())
	})

	t.Run("not_found", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: tmpDir,
		}

		_, err := config.GetDriver(cfg, "nonexistent")
		assert.Error(t, err)
	})

	t.Run("multi_path_env_finds_first_match", func(t *testing.T) {
		dir1 := t.TempDir()
		dir2 := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(dir2, "driver2.toml"),
			[]byte(testManifestTOML),
			0644,
		))

		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: dir1 + string(filepath.ListSeparator) + dir2,
		}

		di, err := config.GetDriver(cfg, "driver2")
		require.NoError(t, err)
		assert.Equal(t, "driver2", di.ID)
	})
}

func TestCreateManifest(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: tmpDir,
		}

		di := makeTestDriverInfo("mydriver", tmpDir)

		err := config.CreateManifest(cfg, di)
		require.NoError(t, err)

		assert.FileExists(t, filepath.Join(tmpDir, "mydriver.toml"))
	})

	t.Run("creates_location_if_absent", func(t *testing.T) {
		newDir := filepath.Join(t.TempDir(), "nonexistent", "subdir")
		cfg := config.Config{
			Level:    config.ConfigEnv,
			Location: newDir,
		}

		di := makeTestDriverInfo("newdriver", newDir)

		err := config.CreateManifest(cfg, di)
		require.NoError(t, err)

		assert.FileExists(t, filepath.Join(newDir, "newdriver.toml"))
	})
}

func TestFindDriverConfigs(t *testing.T) {
	t.Run("empty_location_returns_empty", func(t *testing.T) {
		t.Setenv("ADBC_DRIVER_PATH", "")
		t.Setenv("VIRTUAL_ENV", "")
		t.Setenv("CONDA_PREFIX", "")

		drivers := config.FindDriverConfigs(config.ConfigEnv)
		assert.Empty(t, drivers)
	})

	t.Run("returns_installed_drivers", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("ADBC_DRIVER_PATH", tmpDir)

		require.NoError(t, os.WriteFile(
			filepath.Join(tmpDir, "driver1.toml"),
			[]byte(testManifestTOML),
			0644,
		))
		require.NoError(t, os.WriteFile(
			filepath.Join(tmpDir, "driver2.toml"),
			[]byte(testManifestTOML),
			0644,
		))

		drivers := config.FindDriverConfigs(config.ConfigEnv)
		require.Len(t, drivers, 2)

		ids := make([]string, len(drivers))
		for i, d := range drivers {
			ids[i] = d.ID
		}
		assert.ElementsMatch(t, []string{"driver1", "driver2"}, ids)
	})

	t.Run("nonexistent_path_returns_empty", func(t *testing.T) {
		t.Setenv("ADBC_DRIVER_PATH", "/nonexistent/path/that/does/not/exist")

		drivers := config.FindDriverConfigs(config.ConfigEnv)
		assert.Empty(t, drivers)
	})
}

func TestUninstallDriverShared(t *testing.T) {
	t.Run("dbc_source_removes_driver_dir", func(t *testing.T) {
		tmpDir := t.TempDir()

		driverDir := filepath.Join(tmpDir, "test-driver-1_linux_amd64_v1.0.0")
		require.NoError(t, os.MkdirAll(driverDir, 0755))

		driverPath := filepath.Join(driverDir, "driver.so")
		require.NoError(t, os.WriteFile(driverPath, []byte("fake so"), 0644))

		di := config.DriverInfo{
			ID:       "test-driver-1",
			FilePath: tmpDir,
			Source:   "dbc",
		}
		di.Driver.Shared.Set("linux_amd64", driverPath)

		err := config.UninstallDriverShared(di)
		require.NoError(t, err)

		assert.NoDirExists(t, driverDir)
	})

	t.Run("non_dbc_source_removes_file_only", func(t *testing.T) {
		tmpDir := t.TempDir()

		driverPath := filepath.Join(tmpDir, "driver.so")
		require.NoError(t, os.WriteFile(driverPath, []byte("fake so"), 0644))

		di := config.DriverInfo{
			ID:       "external-driver",
			FilePath: tmpDir,
			Source:   "external",
		}
		di.Driver.Shared.Set("linux_amd64", driverPath)

		err := config.UninstallDriverShared(di)
		require.NoError(t, err)

		assert.NoFileExists(t, driverPath)
	})

	t.Run("missing_file_is_tolerated_for_non_dbc", func(t *testing.T) {
		tmpDir := t.TempDir()

		di := config.DriverInfo{
			ID:       "missing-driver",
			FilePath: tmpDir,
			Source:   "external",
		}
		di.Driver.Shared.Set("linux_amd64", filepath.Join(tmpDir, "nonexistent.so"))

		err := config.UninstallDriverShared(di)
		assert.NoError(t, err)
	})
}
