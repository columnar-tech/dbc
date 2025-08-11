// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

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
		`name = 'Test Driver'
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
