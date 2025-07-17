// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package config

import (
	"os"
	"path/filepath"
	"testing"

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
version = 'v1.6.0'
source = 'dbc'

[ADBC]
version = 'v1.1.0'

[ADBC.features]
supported = ['bulk_insert', 'prepared statement']
unsupported = ['async']

[Driver]
[Driver.shared]
linux_amd64 = '/path/to/majestik/moose/file'
	`), 0644))

	driverInfo, err := loadDriverFromManifest(prefix, driverName)
	require.NoError(t, err)
	require.Equal(t, driverName, driverInfo.ID)
}
