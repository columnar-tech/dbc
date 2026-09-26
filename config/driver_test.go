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

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDriverFromManifest(t *testing.T) {
	prefix := t.TempDir()
	driverName := "test_driver"
	manifestPath := filepath.Join(prefix, driverName+".toml")

	require.NoError(t, os.WriteFile(manifestPath, []byte(`
name = 'BigQuery ADBC Driver'
publisher = 'arrow-adbc'
license = 'Apache-2.0'
version = '1.6.0'
source = 'dbc'

[ADBC]
version = '1.1.0'

[ADBC.features]
supported = ['bulk_insert', 'prepared statement']
unsupported = ['async']

[Driver]
entrypoint = 'AdbcDriverInit'

[Driver.shared]
linux_amd64 = '/path/to/majestik/moose/file'
	`), 0644))

	driverInfo, err := loadDriverFromManifest(prefix, driverName)
	require.NoError(t, err)
	assert.Equal(t, driverName, driverInfo.ID)

	assert.Equal(t, "/path/to/majestik/moose/file", driverInfo.Driver.Shared.Get("linux_amd64"))
}

func TestCreateDriverManifest(t *testing.T) {
	prefix := t.TempDir()
	driverName := "test_driver"
	manifestPath := filepath.Join(prefix, driverName+".toml")

	driverInfo := DriverInfo{
		ID:        driverName,
		Name:      "Test Driver",
		Publisher: "Test Publisher",
		License:   "MIT",
		Version:   semver.MustParse("1.0.0"),
		Source:    "dbc",
	}

	driverInfo.AdbcInfo.Version = semver.MustParse("1.1.0")
	driverInfo.AdbcInfo.Features.Supported = []string{"feature1", "feature2"}
	driverInfo.AdbcInfo.Features.Unsupported = []string{"feature3"}

	driverInfo.Driver.Entrypoint = "AdbcDriverInit"
	driverInfo.Driver.Shared.Set("linux_amd64", "/path/to/driver.so")

	err := createDriverManifest(prefix, driverInfo)
	require.NoError(t, err)

	assert.FileExists(t, manifestPath)
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	assert.Equal(t,
		`manifest_version = 1
name = 'Test Driver'
publisher = 'Test Publisher'
license = 'MIT'
version = '1.0.0'
source = 'dbc'

[ADBC]
version = '1.1.0'

[ADBC.features]
supported = ['feature1', 'feature2']
unsupported = ['feature3']

[Driver]
entrypoint = 'AdbcDriverInit'

[Driver.shared]
linux_amd64 = '/path/to/driver.so'
`, string(data))
}

func TestRemoveManifestSymlinkOnlyRemovesTargetRegistration(t *testing.T) {
	parent := t.TempDir()
	registered := filepath.Join(parent, "registered")
	other := filepath.Join(parent, "other")
	require.NoError(t, os.MkdirAll(registered, 0755))
	require.NoError(t, os.MkdirAll(other, 0755))
	registeredManifest := filepath.Join(registered, "example.toml")
	otherManifest := filepath.Join(other, "example.toml")
	require.NoError(t, os.WriteFile(registeredManifest, []byte("registered"), 0644))
	require.NoError(t, os.WriteFile(otherManifest, []byte("other"), 0644))

	link := filepath.Join(parent, "example.toml")
	if err := os.Symlink(registeredManifest, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	removeManifestSymlink(registered, "example")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("symlink to the requested manifest was not removed: %v", err)
	}

	require.NoError(t, os.Symlink(otherManifest, link))
	removeManifestSymlink(registered, "example")
	linkInfo, err := os.Lstat(link)
	require.NoError(t, err, "symlink to a different manifest should be retained")
	assert.NotZero(t, linkInfo.Mode()&os.ModeSymlink, "retained entry should still be a symlink")
	target, err := os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, otherManifest, target)
}

func TestManifestSymlinkHandlesRelativeNestedLocations(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	location := filepath.Join(root, "nested", "registered")
	parent := filepath.Dir(location)
	manifest := filepath.Join(location, "example.toml")
	otherManifest := filepath.Join(root, "other", "example.toml")
	require.NoError(t, os.MkdirAll(location, 0755))
	require.NoError(t, os.MkdirAll(filepath.Dir(otherManifest), 0755))
	require.NoError(t, os.WriteFile(manifest, []byte("registered"), 0644))
	require.NoError(t, os.WriteFile(otherManifest, []byte("other"), 0644))

	workingDirectory, err := os.Getwd()
	require.NoError(t, err)
	relativeLocation, err := filepath.Rel(workingDirectory, location)
	require.NoError(t, err)
	relativeManifest := filepath.Join(relativeLocation, "example.toml")
	link := filepath.Join(parent, "example.toml")

	if err := os.Symlink(relativeManifest, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	removeManifestSymlink(relativeLocation, "example")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("legacy relative symlink was not removed: %v", err)
	}

	createManifestSymlink(relativeLocation, "example", relativeManifest)
	linkTarget, err := os.Readlink(link)
	require.NoError(t, err)
	if !filepath.IsAbs(linkTarget) {
		linkTarget = filepath.Join(parent, linkTarget)
	}
	actualTarget, err := filepath.Abs(linkTarget)
	require.NoError(t, err)
	expectedTarget, err := filepath.Abs(manifest)
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(expectedTarget), filepath.Clean(actualTarget), "relative nested link should resolve to its manifest")
	removeManifestSymlink(relativeLocation, "example")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("new relative nested symlink was not removed: %v", err)
	}

	otherTarget, err := filepath.Rel(parent, otherManifest)
	require.NoError(t, err)
	require.NoError(t, os.Symlink(otherTarget, link))
	removeManifestSymlink(relativeLocation, "example")
	retainedTarget, err := os.Readlink(link)
	require.NoError(t, err, "symlink to another registration should remain")
	assert.Equal(t, otherTarget, retainedTarget)
}

func TestRemoveManifestSymlinkDoesNotResolveForeignTargetFromWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	location := filepath.Join(root, "nested", "registered")
	manifest := filepath.Join(location, "example.toml")
	parent := filepath.Dir(location)
	require.NoError(t, os.MkdirAll(location, 0755))
	t.Chdir(root)
	require.NoError(t, os.WriteFile(manifest, []byte("registered"), 0644))

	workingDirectory, err := os.Getwd()
	require.NoError(t, err)
	relativeLocation, err := filepath.Rel(workingDirectory, location)
	require.NoError(t, err)
	legacyManifestPath := filepath.Join(relativeLocation, "example.toml")
	foreignTarget := relativeLocation + string(filepath.Separator) + "." + string(filepath.Separator) + "example.toml"
	require.NotEqual(t, legacyManifestPath, foreignTarget)

	expected, err := filepath.Abs(manifest)
	require.NoError(t, err)
	fromWorkingDirectory, err := filepath.Abs(foreignTarget)
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(expected), filepath.Clean(fromWorkingDirectory))
	fromParent, err := filepath.Abs(filepath.Join(parent, foreignTarget))
	require.NoError(t, err)
	assert.NotEqual(t, filepath.Clean(expected), filepath.Clean(fromParent))

	link := filepath.Join(parent, "example.toml")
	if err := os.Symlink(foreignTarget, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	removeManifestSymlink(relativeLocation, "example")
	target, err := os.Readlink(link)
	require.NoError(t, err, "foreign relative symlink should be retained")
	assert.Equal(t, foreignTarget, target)
}

func TestLoadDriverFromUnsupportedManifest(t *testing.T) {
	prefix := t.TempDir()
	driverName := "test_driver"
	manifestPath := filepath.Join(prefix, driverName+".toml")

	require.NoError(t, os.WriteFile(manifestPath, []byte(`
manifest_version = 100

name = 'test_driver'
publisher = 'bar'
license = 'Apache-2.0'
version = '0.1.0'
	`), 0644))

	_, err := loadDriverFromManifest(prefix, driverName)
	require.ErrorContains(t, err, "manifest version 100 is unsupported, only 1 and lower are supported by this version of dbc")
}

func TestLoadDriverFromInvalidManifest(t *testing.T) {
	tests := []struct {
		name        string
		manifest    string
		errContains string
	}{
		{
			name:        "missing name",
			manifest:    `version = '1.0.0'`,
			errContains: "name is required",
		},
		{
			name:        "missing version",
			manifest:    `name = 'Test Driver'`,
			errContains: "version is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix := t.TempDir()
			driverName := "test_driver"
			manifestPath := filepath.Join(prefix, driverName+".toml")

			require.NoError(t, os.WriteFile(manifestPath, []byte(tt.manifest), 0644))

			_, err := loadDriverFromManifest(prefix, driverName)
			require.ErrorIs(t, err, ErrInvalidManifest)
			require.ErrorContains(t, err, tt.errContains)
			require.ErrorContains(t, err, manifestPath)
		})
	}
}
