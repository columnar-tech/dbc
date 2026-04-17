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
	"path/filepath"
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
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

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
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

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
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

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
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

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
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

		cfg, err := loadGlobalConfig(dir)
		assert.Error(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("empty registries section returns empty slice no error", func(t *testing.T) {
		dir := t.TempDir()
		content := `replace_defaults = false
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

		cfg, err := loadGlobalConfig(dir)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Empty(t, cfg.Registries)
		assert.False(t, cfg.ReplaceDefaults)
	})

	t.Run("malformed TOML returns error", func(t *testing.T) {
		dir := t.TempDir()
		content := `[[registries
url = "https://example.com"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

		cfg, err := loadGlobalConfig(dir)
		assert.Error(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("non-http scheme returns error", func(t *testing.T) {
		dir := t.TempDir()
		content := `
[[registries]]
url = "ftp://example.com"
name = "ftp"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

		cfg, err := loadGlobalConfig(dir)
		assert.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "scheme must be http or https")
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

	t.Run("12: non-http scheme entry is silently dropped by toRegistry", func(t *testing.T) {
		result := mergeRegistries(
			[]RegistryEntry{
				{URL: "ftp://example.com", Name: "ftp"},
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

func TestConfigureRegistries(t *testing.T) {
	t.Run("valid config updates registries additively", func(t *testing.T) {
		orig := registries
		origDefault := defaultRegistries
		origGlobal := globalConfig
		defer func() {
			registries = orig
			defaultRegistries = origDefault
			globalConfig = origGlobal
		}()

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`
[[registries]]
url = "https://custom.example.com"
name = "custom"
`), 0600))

		err := ConfigureRegistries(dir)
		require.NoError(t, err)

		assert.Greater(t, len(registries), len(orig))
		found := false
		for _, r := range registries {
			if r.BaseURL != nil && r.BaseURL.String() == "https://custom.example.com" {
				found = true
				break
			}
		}
		assert.True(t, found, "expected custom registry https://custom.example.com to be present in registries")
	})

	t.Run("missing config leaves defaults unchanged", func(t *testing.T) {
		orig := registries
		origDefault := defaultRegistries
		origGlobal := globalConfig
		defer func() {
			registries = orig
			defaultRegistries = origDefault
			globalConfig = origGlobal
		}()

		dir := t.TempDir()
		err := ConfigureRegistries(dir)
		require.NoError(t, err)
		assert.Equal(t, orig, registries)
	})

	t.Run("missing config resets registries to defaults even if previously dirtied", func(t *testing.T) {
		origDefault := defaultRegistries
		origGlobal := globalConfig
		defer func() {
			registries = origDefault
			defaultRegistries = origDefault
			globalConfig = origGlobal
		}()

		registries = append([]Registry{{BaseURL: mustParseURL("https://dirty.example.com")}}, defaultRegistries...)

		dir := t.TempDir()
		err := ConfigureRegistries(dir)
		require.NoError(t, err)
		assert.Equal(t, defaultRegistries, registries)
	})

	t.Run("DBC_BASE_URL set makes ConfigureRegistries a no-op", func(t *testing.T) {
		orig := registries
		origDefault := defaultRegistries
		origGlobal := globalConfig
		defer func() {
			registries = orig
			defaultRegistries = origDefault
			globalConfig = origGlobal
		}()
		t.Setenv("DBC_BASE_URL", "https://override.example.com")

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`
[[registries]]
url = "https://custom.example.com"
`), 0600))

		err := ConfigureRegistries(dir)
		require.NoError(t, err)
		assert.Equal(t, orig, registries)
	})

	t.Run("invalid URL in config returns error", func(t *testing.T) {
		orig := registries
		origDefault := defaultRegistries
		origGlobal := globalConfig
		defer func() {
			registries = orig
			defaultRegistries = origDefault
			globalConfig = origGlobal
		}()

		sentinel := Registry{BaseURL: mustParseURL("https://sentinel.example.com")}
		registries = append([]Registry{sentinel}, defaultRegistries...)

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`
[[registries]]
url = "http://bad url with spaces"
`), 0600))

		err := ConfigureRegistries(dir)
		assert.Error(t, err)
		require.Len(t, registries, len(defaultRegistries)+1)
		assert.Equal(t, "https://sentinel.example.com", registries[0].BaseURL.String())
	})

	t.Run("replace_defaults true omits defaults", func(t *testing.T) {
		orig := registries
		origDefault := defaultRegistries
		origGlobal := globalConfig
		defer func() {
			registries = orig
			defaultRegistries = origDefault
			globalConfig = origGlobal
		}()

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`
replace_defaults = true

[[registries]]
url = "https://custom.example.com"
name = "custom"
`), 0600))

		err := ConfigureRegistries(dir)
		require.NoError(t, err)
		assert.Len(t, registries, 1)
		assert.Equal(t, "https://custom.example.com", registries[0].BaseURL.String())
	})
}

func TestSetProjectRegistries(t *testing.T) {
	saveAndRestore := func(t *testing.T) {
		t.Helper()
		origReg := registries
		origDefault := defaultRegistries
		origGlobal := globalConfig
		t.Cleanup(func() {
			registries = origReg
			defaultRegistries = origDefault
			globalConfig = origGlobal
		})
	}

	t.Run("project registries prepended before defaults", func(t *testing.T) {
		saveAndRestore(t)
		defaultRegistries = []Registry{
			{BaseURL: mustParseURL("https://default1.example.com")},
		}
		globalConfig = nil

		err := SetProjectRegistries([]RegistryEntry{{URL: "https://project.example.com", Name: "project"}}, nil)
		require.NoError(t, err)
		require.Len(t, registries, 2)
		assert.Equal(t, "https://project.example.com", registries[0].BaseURL.String())
		assert.Equal(t, "https://default1.example.com", registries[1].BaseURL.String())
	})

	t.Run("project with global config merges correctly", func(t *testing.T) {
		saveAndRestore(t)
		defaultRegistries = []Registry{
			{BaseURL: mustParseURL("https://default.example.com")},
		}
		globalConfig = &GlobalConfig{
			Registries: []RegistryEntry{{URL: "https://global.example.com", Name: "global"}},
		}

		err := SetProjectRegistries([]RegistryEntry{{URL: "https://project.example.com", Name: "project"}}, nil)
		require.NoError(t, err)
		require.Len(t, registries, 3)
		assert.Equal(t, "https://project.example.com", registries[0].BaseURL.String())
		assert.Equal(t, "https://global.example.com", registries[1].BaseURL.String())
		assert.Equal(t, "https://default.example.com", registries[2].BaseURL.String())
	})

	t.Run("replace_defaults true omits defaults", func(t *testing.T) {
		saveAndRestore(t)
		defaultRegistries = []Registry{
			{BaseURL: mustParseURL("https://default.example.com")},
		}
		globalConfig = nil
		replaceTrue := true
		err := SetProjectRegistries([]RegistryEntry{{URL: "https://project.example.com"}}, &replaceTrue)
		require.NoError(t, err)
		require.Len(t, registries, 1)
		assert.Equal(t, "https://project.example.com", registries[0].BaseURL.String())
	})

	t.Run("DBC_BASE_URL set makes SetProjectRegistries a no-op", func(t *testing.T) {
		saveAndRestore(t)
		t.Setenv("DBC_BASE_URL", "https://override.example.com")
		orig := registries

		err := SetProjectRegistries([]RegistryEntry{{URL: "https://project.example.com"}}, nil)
		require.NoError(t, err)
		assert.Equal(t, orig, registries)
	})

	t.Run("hostless URL returns error", func(t *testing.T) {
		saveAndRestore(t)
		err := SetProjectRegistries([]RegistryEntry{{URL: "my-registry"}}, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing host")
	})

	t.Run("non-http scheme returns error", func(t *testing.T) {
		saveAndRestore(t)
		err := SetProjectRegistries([]RegistryEntry{{URL: "ftp://example.com"}}, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "scheme must be http or https")
	})

	t.Run("empty URL returns error", func(t *testing.T) {
		saveAndRestore(t)
		err := SetProjectRegistries([]RegistryEntry{{URL: ""}}, nil)
		assert.Error(t, err)
	})
}

func TestConfigureRegistriesThenSetProjectRegistries(t *testing.T) {
	origDefault := defaultRegistries
	origGlobal := globalConfig
	t.Cleanup(func() {
		registries = origDefault
		defaultRegistries = origDefault
		globalConfig = origGlobal
	})

	registries = defaultRegistries
	globalConfig = nil

	// Set up a global config with one registry
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`
[[registries]]
url = "https://global.example.com"
name = "global"
`), 0600))

	require.NoError(t, ConfigureRegistries(dir))

	// Now set project registries on top
	err := SetProjectRegistries([]RegistryEntry{{URL: "https://project.example.com", Name: "project"}}, nil)
	require.NoError(t, err)

	// Expected order: project → global → defaults
	require.Equal(t, len(defaultRegistries)+2, len(registries))
	assert.Equal(t, "https://project.example.com", registries[0].BaseURL.String())
	found := false
	for _, r := range registries {
		if r.BaseURL != nil && r.BaseURL.String() == "https://global.example.com" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected global registry to be present after SetProjectRegistries")
}
