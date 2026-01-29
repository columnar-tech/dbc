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
