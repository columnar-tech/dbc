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

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/columnar-tech/dbc/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const findInManifestTOML = `
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

func TestFindDriverConfigsIn(t *testing.T) {
	t.Run("lists drivers from an explicit location without env", func(t *testing.T) {
		t.Setenv("ADBC_DRIVER_PATH", "/should/not/be/read")
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "drv1.toml"), []byte(findInManifestTOML), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "drv2.toml"), []byte(findInManifestTOML), 0o644))

		got := config.FindDriverConfigsIn(dir)
		ids := make([]string, len(got))
		for i, d := range got {
			ids[i] = d.ID
		}
		assert.ElementsMatch(t, []string{"drv1", "drv2"}, ids)
	})

	t.Run("empty location returns nil", func(t *testing.T) {
		assert.Empty(t, config.FindDriverConfigsIn(""))
	})

	t.Run("nonexistent location returns empty", func(t *testing.T) {
		assert.Empty(t, config.FindDriverConfigsIn(filepath.Join(t.TempDir(), "nope")))
	})
}
