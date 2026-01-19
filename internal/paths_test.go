// Copyright 2025 Columnar Technologies Inc.
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

package internal

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCredentialPath(t *testing.T) {
	t.Run("honors XDG_DATA_HOME when set to an absolute path", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("XDG_DATA_HOME", tmpDir)

		path, err := GetCredentialPath()
		require.NoError(t, err)
		expected := filepath.Join(tmpDir, "dbc", "credentials", "credentials.toml")
		assert.Equal(t, expected, path)
	})

	t.Run("errors if XDG_DATA_HOME is set to a relative path", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "any/relative/path")

		_, err := GetCredentialPath()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "path in $XDG_DATA_HOME is relative")
	})

	t.Run("default behavior for each platform", func(t *testing.T) {
		switch runtime.GOOS {
		case "windows":
			appData := os.Getenv("LocalAppData")
			if appData == "" {
				t.Errorf("failed to get LocalAppData")
			}
			path, err := GetCredentialPath()
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(appData, "dbc", "credentials", "credentials.toml"), path)

		case "darwin":
			userHome, err := os.UserHomeDir()
			if err != nil {
				t.Errorf("failed to get user home directory")
			}

			path, err := GetCredentialPath()
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(userHome, "Library", "Application Support", "Columnar", "dbc", "credentials", "credentials.toml"), path)

		default:
			userHome, err := os.UserHomeDir()
			if err != nil {
				t.Errorf("failed to get user home directory")
			}

			path, err := GetCredentialPath()
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(userHome, ".local", "share", "dbc", "credentials", "credentials.toml"), path)
		}
	})
}
