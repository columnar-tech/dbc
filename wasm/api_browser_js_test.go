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

//go:build js && !dbcnode

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/columnar-tech/dbc/auth"
	"github.com/stretchr/testify/require"
)

func isolateBrowserConfig(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("XDG_DATA_HOME", root)
	t.Setenv("XDG_CACHE_HOME", root)
	restore := auth.SetCredPathForTesting(filepath.Join(root, "credentials.toml"))
	auth.ResetCredentialsForTesting()
	t.Cleanup(func() {
		auth.ResetCredentialsForTesting()
		restore()
	})
	configDir := filepath.Join(root, "columnar", "dbc")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	// A filesystem-backed default would fail to parse this file. Browser
	// construction must not read it, even when the host supplies a filesystem.
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("invalid TOML ["), 0o600))
}

func TestBrowserClientSkipsDefaultRegistryConfig(t *testing.T) {
	isolateBrowserConfig(t)
	for _, cfg := range []string{"", `{}`, `{"registryConfig":""}`, `{"registryConfig":"none"}`} {
		t.Run(cfg, func(t *testing.T) {
			client, err := newFetchClient(cfg)
			require.NoError(t, err)
			require.NotEmpty(t, client.Registries())
		})
	}
}

func TestBrowserClientRejectsExplicitFileConfig(t *testing.T) {
	isolateBrowserConfig(t)
	for _, tc := range []struct {
		name string
		cfg  string
		want string
	}{
		{"global", `{"registryConfig":"default"}`, "Node"},
		{"project", `{"projectFile":"missing.toml"}`, "Node"},
		{"project with none", `{"registryConfig":"none","projectFile":"missing.toml"}`, "Node"},
		{"unknown mode", `{"registryConfig":"invalid"}`, "registryConfig"},
		{"credential store", `{"credentialStore":"default"}`, "Node"},
		{"credential store with registries", `{"registries":[{"url":"https://example.test"}],"credentialStore":"default"}`, "Node"},
		{"unknown credential store", `{"credentialStore":"invalid"}`, "credentialStore"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newFetchClient(tc.cfg)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestBrowserExplicitRegistriesIgnoreFileConfig(t *testing.T) {
	isolateBrowserConfig(t)
	for _, cfg := range []string{
		`{"baseURL":"https://example.test","registryConfig":"default","projectFile":"missing.toml"}`,
		`{"registries":[{"url":"https://example.test","name":"explicit"}],"registryConfig":"default","projectFile":"missing.toml"}`,
		`{"registries":[{"url":"https://example.test"}],"registryConfig":"invalid","projectFile":"missing.toml"}`,
	} {
		t.Run(cfg, func(t *testing.T) {
			client, err := newFetchClient(cfg)
			require.NoError(t, err)
			require.Len(t, client.Registries(), 1)
			require.Equal(t, "https://example.test", client.Registries()[0].BaseURL.String())
		})
	}
}

func TestBrowserEmptyRegistriesIgnoreFileConfig(t *testing.T) {
	isolateBrowserConfig(t)
	client, err := newFetchClient(`{"registries":[],"registryConfig":"default","projectFile":"missing.toml"}`)
	require.NoError(t, err)
	require.Empty(t, client.Registries())
}
