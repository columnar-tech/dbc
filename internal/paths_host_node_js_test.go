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

//go:build js && dbcnode

package internal

import (
	"syscall/js"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNodeHostPaths(t *testing.T) {
	for _, tc := range []struct {
		name       string
		platform   string
		env        map[string]string
		configDir  string
		configPath string
		credential string
		configErr  string
		credErr    string
	}{
		{
			name: "linux defaults", platform: "linux",
			env:        map[string]string{"HOME": "/home/me"},
			configDir:  "/home/me/.config",
			configPath: "/home/me/.config/columnar/dbc",
			credential: "/home/me/.local/share/dbc/credentials/credentials.toml",
		},
		{
			name: "linux XDG overrides", platform: "linux",
			env:        map[string]string{"HOME": "/home/me", "XDG_CONFIG_HOME": "/config", "XDG_DATA_HOME": "/data"},
			configDir:  "/config",
			configPath: "/config/columnar/dbc",
			credential: "/data/dbc/credentials/credentials.toml",
		},
		{
			name: "macOS ignores XDG config", platform: "darwin",
			env:        map[string]string{"HOME": "/Users/me", "XDG_CONFIG_HOME": "/ignored"},
			configDir:  "/Users/me/Library/Application Support",
			configPath: "/Users/me/Library/Application Support/Columnar/dbc",
			credential: "/Users/me/Library/Application Support/Columnar/dbc/credentials/credentials.toml",
		},
		{
			name: "macOS honors XDG data", platform: "darwin",
			env:        map[string]string{"HOME": "/Users/me", "XDG_CONFIG_HOME": "ignored-relative", "XDG_DATA_HOME": "/data"},
			configDir:  "/Users/me/Library/Application Support",
			configPath: "/Users/me/Library/Application Support/Columnar/dbc",
			credential: "/data/dbc/credentials/credentials.toml",
		},
		{
			name: "macOS requires HOME even with XDG config", platform: "darwin",
			env:       map[string]string{"XDG_CONFIG_HOME": "/ignored"},
			configErr: "$HOME is not defined",
			credErr:   "$HOME is not defined",
		},
		{
			name: "Windows native app data ignores XDG config", platform: "win32",
			env:        map[string]string{"HOME": "C:/Users/me", "AppData": "C:/Roaming", "LocalAppData": "C:/Local", "XDG_CONFIG_HOME": "X:/ignored"},
			configDir:  "C:/Roaming",
			configPath: "C:/Roaming/Columnar/dbc",
			credential: "C:/Local/dbc/credentials/credentials.toml",
		},
		{
			name: "Windows XDG data drive override", platform: "win32",
			env:        map[string]string{"AppData": "C:/Roaming", "LocalAppData": "C:/Local", "XDG_DATA_HOME": "X:/data"},
			configDir:  "C:/Roaming",
			configPath: "C:/Roaming/Columnar/dbc",
			credential: "X:/data/dbc/credentials/credentials.toml",
		},
		{
			name: "Windows UNC roots survive joins", platform: "win32",
			env:        map[string]string{"AppData": `\\server/share/config`, "XDG_DATA_HOME": `\\server/share/data`},
			configDir:  `\\server/share/config`,
			configPath: `\\server/share/config/Columnar/dbc`,
			credential: `\\server/share/data/dbc/credentials/credentials.toml`,
		},
		{
			name: "Windows requires native app data", platform: "win32",
			env:       map[string]string{"HOME": "C:/Users/me", "XDG_CONFIG_HOME": "/ignored"},
			configErr: "%AppData% is not defined",
			credErr:   "%LocalAppData% is not set",
		},
		{
			name: "Windows drive-relative data rejected", platform: "win32",
			env:        map[string]string{"AppData": "C:/Roaming", "XDG_DATA_HOME": "X:data"},
			configDir:  "C:/Roaming",
			configPath: "C:/Roaming/Columnar/dbc",
			credErr:    "path in $XDG_DATA_HOME is relative",
		},
		{
			name: "Windows rooted data without volume rejected", platform: "win32",
			env:        map[string]string{"AppData": "C:/Roaming", "XDG_DATA_HOME": "/data"},
			configDir:  "C:/Roaming",
			configPath: "C:/Roaming/Columnar/dbc",
			credErr:    "path in $XDG_DATA_HOME is relative",
		},
		{
			name: "macOS relative XDG data rejected", platform: "darwin",
			env:        map[string]string{"HOME": "/Users/me", "XDG_DATA_HOME": "data"},
			configDir:  "/Users/me/Library/Application Support",
			configPath: "/Users/me/Library/Application Support/Columnar/dbc",
			credErr:    "path in $XDG_DATA_HOME is relative",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			object := js.Global().Get("Object")
			process := js.Global().Get("process")
			descriptor := object.Call("getOwnPropertyDescriptor", process, "platform")
			object.Call("defineProperty", process, "platform", map[string]any{"value": tc.platform, "configurable": true})
			t.Cleanup(func() {
				object.Call("defineProperty", process, "platform", descriptor)
			})
			for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "AppData", "LocalAppData"} {
				t.Setenv(key, tc.env[key])
			}
			configDir, dirErr := UserConfigDir()
			configPath, configErr := GetUserConfigPath()
			if tc.configErr != "" {
				require.ErrorContains(t, dirErr, tc.configErr)
				require.ErrorContains(t, configErr, tc.configErr)
			} else {
				require.NoError(t, dirErr)
				require.NoError(t, configErr)
				require.Equal(t, tc.configDir, configDir)
				require.Equal(t, tc.configPath, configPath)
			}
			credential, err := GetCredentialPath()
			if tc.credErr != "" {
				require.ErrorContains(t, err, tc.credErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.credential, credential)
			}
		})
	}
}
