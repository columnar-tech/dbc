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

package dbc

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadGlobalConfig(t *testing.T) {
	t.Run("valid config with two registries", func(t *testing.T) {
		dir := t.TempDir()
		content := `
[[registries]]
url = "https://example.com/registry"
name = "example"

[[registries]]
url = "https://other.example.org"
`
		require.NoError(t, os.WriteFile(dir+"/config.toml", []byte(content), 0600))

		cfg, err := loadGlobalConfig(dir)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Len(t, cfg.Registries, 2)
		assert.Equal(t, "https://example.com/registry", cfg.Registries[0].URL)
		assert.Equal(t, "example", cfg.Registries[0].Name)
		assert.Equal(t, "https://other.example.org", cfg.Registries[1].URL)
		assert.Equal(t, "", cfg.Registries[1].Name)
		assert.False(t, cfg.ReplaceDefaults)
	})

	t.Run("valid config with replace_defaults true", func(t *testing.T) {
		dir := t.TempDir()
		content := `
replace_defaults = true

[[registries]]
url = "https://custom.registry.io"
name = "custom"
`
		require.NoError(t, os.WriteFile(dir+"/config.toml", []byte(content), 0600))

		cfg, err := loadGlobalConfig(dir)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.True(t, cfg.ReplaceDefaults)
		assert.Len(t, cfg.Registries, 1)
	})

	t.Run("config with only registries no replace_defaults", func(t *testing.T) {
		dir := t.TempDir()
		content := `
[[registries]]
url = "https://registry.example.com"
name = "myregistry"
`
		require.NoError(t, os.WriteFile(dir+"/config.toml", []byte(content), 0600))

		cfg, err := loadGlobalConfig(dir)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.False(t, cfg.ReplaceDefaults)
		assert.Len(t, cfg.Registries, 1)
	})

	t.Run("missing config.toml returns nil nil", func(t *testing.T) {
		dir := t.TempDir()

		cfg, err := loadGlobalConfig(dir)
		assert.NoError(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("config with invalid URL returns error no panic", func(t *testing.T) {
		dir := t.TempDir()
		content := `
[[registries]]
url = "http://bad url with spaces"
name = "bad"
`
		require.NoError(t, os.WriteFile(dir+"/config.toml", []byte(content), 0600))

		cfg, err := loadGlobalConfig(dir)
		assert.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "http://bad url with spaces")
	})

	t.Run("config with empty url field returns error", func(t *testing.T) {
		dir := t.TempDir()
		content := `
[[registries]]
url = ""
name = "nourl"
`
		require.NoError(t, os.WriteFile(dir+"/config.toml", []byte(content), 0600))

		cfg, err := loadGlobalConfig(dir)
		assert.Error(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("empty registries section returns empty slice no error", func(t *testing.T) {
		dir := t.TempDir()
		content := `replace_defaults = false
`
		require.NoError(t, os.WriteFile(dir+"/config.toml", []byte(content), 0600))

		cfg, err := loadGlobalConfig(dir)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Empty(t, cfg.Registries)
		assert.False(t, cfg.ReplaceDefaults)
	})
}
