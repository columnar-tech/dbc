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
	"net/url"
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

func TestMergeRegistries(t *testing.T) {
	makeReg := func(t *testing.T, rawURL string, name ...string) Registry {
		t.Helper()
		u, err := url.Parse(rawURL)
		require.NoError(t, err)
		n := ""
		if len(name) > 0 {
			n = name[0]
		}
		return Registry{Name: n, BaseURL: u}
	}

	entry := func(rawURL, name string) RegistryEntry {
		return RegistryEntry{URL: rawURL, Name: name}
	}

	boolPtr := func(b bool) *bool { return &b }

	d1 := makeReg(t, "https://default1.example.com", "default1")
	d2 := makeReg(t, "https://default2.example.com", "default2")
	defaults := []Registry{d1, d2}

	t.Run("1: full merge project+global+defaults", func(t *testing.T) {
		result := mergeRegistries(
			[]RegistryEntry{entry("https://project.example.com", "project")},
			nil,
			[]RegistryEntry{entry("https://global.example.com", "global")},
			false,
			defaults,
		)
		require.Len(t, result, 4)
		assert.Equal(t, "https://project.example.com", result[0].BaseURL.String())
		assert.Equal(t, "https://global.example.com", result[1].BaseURL.String())
		assert.Equal(t, "https://default1.example.com", result[2].BaseURL.String())
		assert.Equal(t, "https://default2.example.com", result[3].BaseURL.String())
	})

	t.Run("2: empty project uses global+defaults", func(t *testing.T) {
		result := mergeRegistries(
			nil,
			nil,
			[]RegistryEntry{entry("https://global.example.com", "global")},
			false,
			defaults,
		)
		require.Len(t, result, 3)
		assert.Equal(t, "https://global.example.com", result[0].BaseURL.String())
		assert.Equal(t, "https://default1.example.com", result[1].BaseURL.String())
		assert.Equal(t, "https://default2.example.com", result[2].BaseURL.String())
	})

	t.Run("3: empty global uses project+defaults", func(t *testing.T) {
		result := mergeRegistries(
			[]RegistryEntry{entry("https://project.example.com", "project")},
			nil,
			nil,
			false,
			defaults,
		)
		require.Len(t, result, 3)
		assert.Equal(t, "https://project.example.com", result[0].BaseURL.String())
		assert.Equal(t, "https://default1.example.com", result[1].BaseURL.String())
		assert.Equal(t, "https://default2.example.com", result[2].BaseURL.String())
	})

	t.Run("4: both project and global empty returns only defaults", func(t *testing.T) {
		result := mergeRegistries(nil, nil, nil, false, defaults)
		require.Len(t, result, 2)
		assert.Equal(t, "https://default1.example.com", result[0].BaseURL.String())
		assert.Equal(t, "https://default2.example.com", result[1].BaseURL.String())
	})

	t.Run("5: project replace_defaults=true omits defaults", func(t *testing.T) {
		result := mergeRegistries(
			[]RegistryEntry{entry("https://project.example.com", "project")},
			boolPtr(true),
			[]RegistryEntry{entry("https://global.example.com", "global")},
			false,
			defaults,
		)
		require.Len(t, result, 2)
		assert.Equal(t, "https://project.example.com", result[0].BaseURL.String())
		assert.Equal(t, "https://global.example.com", result[1].BaseURL.String())
	})

	t.Run("6: global replace_defaults=true (no project setting) omits defaults", func(t *testing.T) {
		result := mergeRegistries(
			nil,
			nil,
			[]RegistryEntry{entry("https://global.example.com", "global")},
			true,
			defaults,
		)
		require.Len(t, result, 1)
		assert.Equal(t, "https://global.example.com", result[0].BaseURL.String())
	})

	t.Run("7: project replace_defaults=false overrides global replace_defaults=true", func(t *testing.T) {
		result := mergeRegistries(
			[]RegistryEntry{entry("https://project.example.com", "project")},
			boolPtr(false),
			[]RegistryEntry{entry("https://global.example.com", "global")},
			true,
			defaults,
		)
		require.Len(t, result, 4)
		assert.Equal(t, "https://project.example.com", result[0].BaseURL.String())
		assert.Equal(t, "https://global.example.com", result[1].BaseURL.String())
		assert.Equal(t, "https://default1.example.com", result[2].BaseURL.String())
		assert.Equal(t, "https://default2.example.com", result[3].BaseURL.String())
	})

	t.Run("8: dedup same URL in project and global keeps project entry", func(t *testing.T) {
		result := mergeRegistries(
			[]RegistryEntry{entry("https://shared.example.com", "from-project")},
			nil,
			[]RegistryEntry{entry("https://shared.example.com", "from-global")},
			false,
			defaults,
		)
		require.Len(t, result, 3)
		assert.Equal(t, "from-project", result[0].Name)
		assert.Equal(t, "https://shared.example.com", result[0].BaseURL.String())
	})

	t.Run("9: URL normalization trailing slash dedup", func(t *testing.T) {
		result := mergeRegistries(
			[]RegistryEntry{entry("https://shared.example.com/path/", "from-project")},
			nil,
			[]RegistryEntry{entry("https://shared.example.com/path", "from-global")},
			false,
			[]Registry{},
		)
		require.Len(t, result, 1)
		assert.Equal(t, "from-project", result[0].Name)
	})

	t.Run("10: dedup between global and defaults", func(t *testing.T) {
		result := mergeRegistries(
			nil,
			nil,
			[]RegistryEntry{entry("https://default1.example.com", "global-override")},
			false,
			defaults,
		)
		require.Len(t, result, 2)
		assert.Equal(t, "global-override", result[0].Name)
		assert.Equal(t, "https://default1.example.com", result[0].BaseURL.String())
		assert.Equal(t, "https://default2.example.com", result[1].BaseURL.String())
	})

	t.Run("11: invalid URL entry is skipped", func(t *testing.T) {
		result := mergeRegistries(
			[]RegistryEntry{
				{URL: "http://bad url with spaces", Name: "bad"},
				{URL: "https://good.example.com", Name: "good"},
			},
			nil,
			nil,
			false,
			[]Registry{},
		)
		require.Len(t, result, 1)
		assert.Equal(t, "good", result[0].Name)
	})
}
