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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Directory for dbc config, uuid/machine-ids but only for credentials on macOS.
// See GetCredentialPath for credentials behavior.
func GetUserConfigPath() (string, error) {
	userdir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get dbc config dir: %v", err)
	}

	// Capitalize Columnar on Windows and macOS for consistent style
	dirname := "columnar"
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		dirname = "Columnar"
	}

	finalDir := filepath.Join(userdir, dirname, "dbc")

	return finalDir, nil
}

// Directory for dbc credentials. This dir is distinct from GetUserConfigPath
// except for on macOS where it's the same
func GetCredentialPath() (string, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		switch runtime.GOOS {
		case "windows":
			dir = os.Getenv("LocalAppData")
			if dir == "" {
				return "", errors.New("%LocalAppData% is not set")
			}
		case "darwin":
			// On macOS, use the same base directory structure as the config
			userdir, err := GetUserConfigPath()
			if err != nil {
				return "", fmt.Errorf("failed to get user config directory: %w", err)
			}
			return filepath.Join(userdir, "credentials", "credentials.toml"), nil
		default: // unix
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get user home directory: %w", err)
			}
			dir = filepath.Join(home, ".local", "share")
		}
	} else if !filepath.IsAbs(dir) {
		return "", errors.New("path in $XDG_DATA_HOME is relative")
	}

	return filepath.Join(dir, "dbc", "credentials", "credentials.toml"), nil
}
