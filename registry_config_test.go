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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

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

		cfg, err := LoadGlobalConfig(dir)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Len(t, cfg.Registries, 2)
		assert.Equal(t, "https://example.com/registry", cfg.Registries[0].URL)
		assert.Equal(t, "example", cfg.Registries[0].Name)
		assert.Equal(t, "https://other.example.org", cfg.Registries[1].URL)
		assert.Empty(t, cfg.Registries[1].Name)
		assert.False(t, cfg.ReplaceDefaults)
	})

	t.Run("replace_defaults true", func(t *testing.T) {
		dir := t.TempDir()
		content := `
replace_defaults = true

[[registries]]
url = "https://custom.registry.io"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

		cfg, err := LoadGlobalConfig(dir)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.True(t, cfg.ReplaceDefaults)
	})

	t.Run("missing config.toml returns nil,nil", func(t *testing.T) {
		cfg, err := LoadGlobalConfig(t.TempDir())
		assert.NoError(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("invalid URL rejected", func(t *testing.T) {
		dir := t.TempDir()
		content := `[[registries]]
url = "http://bad url with spaces"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

		cfg, err := LoadGlobalConfig(dir)
		assert.Error(t, err)
		assert.Nil(t, cfg)
	})

	t.Run("empty URL rejected", func(t *testing.T) {
		dir := t.TempDir()
		content := `[[registries]]
url = ""
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

		_, err := LoadGlobalConfig(dir)
		assert.Error(t, err)
	})

	t.Run("non-http scheme rejected", func(t *testing.T) {
		dir := t.TempDir()
		content := `[[registries]]
url = "ftp://example.com"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

		_, err := LoadGlobalConfig(dir)
		assert.ErrorContains(t, err, "scheme must be http or https")
	})

	t.Run("missing host rejected", func(t *testing.T) {
		dir := t.TempDir()
		content := `[[registries]]
url = "https:///onlypath"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

		_, err := LoadGlobalConfig(dir)
		assert.ErrorContains(t, err, "missing host")
	})

	t.Run("replace_defaults=true with no entries accepted (project config may supply them)", func(t *testing.T) {
		dir := t.TempDir()
		content := `replace_defaults = true
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

		cfg, err := LoadGlobalConfig(dir)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.True(t, cfg.ReplaceDefaults)
		assert.Empty(t, cfg.Registries)
	})

	t.Run("malformed TOML rejected", func(t *testing.T) {
		dir := t.TempDir()
		content := `[[registries
url = "https://example.com"
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600))

		_, err := LoadGlobalConfig(dir)
		assert.Error(t, err)
	})
}

func TestMergeRegistries(t *testing.T) {
	defaults := []Registry{
		{BaseURL: mustParseURL("https://default-a.example.com")},
		{BaseURL: mustParseURL("https://default-b.example.com")},
	}

	t.Run("no entries returns defaults", func(t *testing.T) {
		got := mergeRegistries(nil, nil, nil, false, defaults)
		require.Len(t, got, 2)
		assert.Equal(t, "https://default-a.example.com", got[0].BaseURL.String())
	})

	t.Run("global registries prepended before defaults", func(t *testing.T) {
		global := []RegistryEntry{{URL: "https://global.example.com", Name: "g"}}
		got := mergeRegistries(nil, nil, global, false, defaults)
		require.Len(t, got, 3)
		assert.Equal(t, "https://global.example.com", got[0].BaseURL.String())
		assert.Equal(t, "g", got[0].Name)
	})

	t.Run("project registries prepended before global and defaults", func(t *testing.T) {
		project := []RegistryEntry{{URL: "https://proj.example.com"}}
		global := []RegistryEntry{{URL: "https://glob.example.com"}}
		got := mergeRegistries(project, nil, global, false, defaults)
		require.Len(t, got, 4)
		assert.Equal(t, "https://proj.example.com", got[0].BaseURL.String())
		assert.Equal(t, "https://glob.example.com", got[1].BaseURL.String())
	})

	t.Run("project replace_defaults drops defaults only", func(t *testing.T) {
		project := []RegistryEntry{{URL: "https://proj.example.com"}}
		global := []RegistryEntry{{URL: "https://glob.example.com"}}
		got := mergeRegistries(project, boolPtr(true), global, false, defaults)
		require.Len(t, got, 2)
		assert.Equal(t, "https://proj.example.com", got[0].BaseURL.String())
		assert.Equal(t, "https://glob.example.com", got[1].BaseURL.String())
	})

	t.Run("global replace_defaults honored when project doesn't override", func(t *testing.T) {
		got := mergeRegistries(nil, nil, []RegistryEntry{{URL: "https://only.example.com"}}, true, defaults)
		require.Len(t, got, 1)
		assert.Equal(t, "https://only.example.com", got[0].BaseURL.String())
	})

	t.Run("project replace_defaults=false overrides global true", func(t *testing.T) {
		got := mergeRegistries(nil, boolPtr(false), []RegistryEntry{{URL: "https://g.example.com"}}, true, defaults)
		require.Len(t, got, 3)
	})

	t.Run("duplicate URLs deduplicated, first wins", func(t *testing.T) {
		project := []RegistryEntry{{URL: "https://dup.example.com", Name: "project"}}
		global := []RegistryEntry{{URL: "https://dup.example.com", Name: "global"}}
		got := mergeRegistries(project, nil, global, false, defaults)
		require.Len(t, got, 3)
		assert.Equal(t, "project", got[0].Name)
	})

	t.Run("invalid entries silently skipped", func(t *testing.T) {
		project := []RegistryEntry{
			{URL: "not a url"},
			{URL: "ftp://example.com"},
			{URL: "https://good.example.com"},
		}
		got := mergeRegistries(project, nil, nil, false, defaults)
		require.Len(t, got, 3)
		assert.Equal(t, "https://good.example.com", got[0].BaseURL.String())
	})
}

func TestNewClientWithRegistryOptions(t *testing.T) {
	t.Run("WithProjectRegistries merges with defaults", func(t *testing.T) {
		t.Setenv("DBC_BASE_URL", "")
		c, err := NewClient(WithProjectRegistries([]RegistryEntry{{URL: "https://proj.example.com"}}, nil))
		require.NoError(t, err)
		regs := c.Registries()
		require.GreaterOrEqual(t, len(regs), 1)
		assert.Equal(t, "https://proj.example.com", regs[0].BaseURL.String())
	})

	t.Run("WithProjectRegistries + replace_defaults returns only project", func(t *testing.T) {
		t.Setenv("DBC_BASE_URL", "")
		c, err := NewClient(WithProjectRegistries([]RegistryEntry{{URL: "https://only.example.com"}}, boolPtr(true)))
		require.NoError(t, err)
		regs := c.Registries()
		require.Len(t, regs, 1)
		assert.Equal(t, "https://only.example.com", regs[0].BaseURL.String())
	})

	t.Run("WithGlobalConfig merges global entries", func(t *testing.T) {
		t.Setenv("DBC_BASE_URL", "")
		cfg := &GlobalConfig{Registries: []RegistryEntry{{URL: "https://glob.example.com"}}}
		c, err := NewClient(WithGlobalConfig(cfg))
		require.NoError(t, err)
		regs := c.Registries()
		require.GreaterOrEqual(t, len(regs), 1)
		assert.Equal(t, "https://glob.example.com", regs[0].BaseURL.String())
	})

	t.Run("WithBaseURL overrides registry options", func(t *testing.T) {
		c, err := NewClient(
			WithBaseURL("https://only-base.example.com"),
			WithGlobalConfig(&GlobalConfig{Registries: []RegistryEntry{{URL: "https://ignored.example.com"}}}),
			WithProjectRegistries([]RegistryEntry{{URL: "https://ignored2.example.com"}}, nil),
		)
		require.NoError(t, err)
		regs := c.Registries()
		require.Len(t, regs, 1)
		assert.Equal(t, "https://only-base.example.com", regs[0].BaseURL.String())
	})

	t.Run("WithProjectRegistries rejects invalid URL at NewClient time", func(t *testing.T) {
		_, err := NewClient(WithProjectRegistries([]RegistryEntry{{URL: ""}}, nil))
		assert.ErrorContains(t, err, "empty url")
	})

	t.Run("project replace_defaults=true with no entries rejected only if merge is empty", func(t *testing.T) {
		// No global registries, no project registries, drop defaults: truly empty.
		_, err := NewClient(WithProjectRegistries(nil, boolPtr(true)))
		assert.ErrorContains(t, err, "empty registry list")
	})

	t.Run("project replace_defaults=true with no project entries keeps global entries", func(t *testing.T) {
		cfg := &GlobalConfig{Registries: []RegistryEntry{{URL: "https://glob.example.com"}}}
		c, err := NewClient(WithGlobalConfig(cfg), WithProjectRegistries(nil, boolPtr(true)))
		require.NoError(t, err)
		regs := c.Registries()
		require.Len(t, regs, 1, "project replace_defaults drops only built-in defaults, not global entries")
		assert.Equal(t, "https://glob.example.com", regs[0].BaseURL.String())
	})

	t.Run("WithProjectRegistries rejects non-http scheme", func(t *testing.T) {
		_, err := NewClient(WithProjectRegistries([]RegistryEntry{{URL: "ftp://example.com"}}, nil))
		assert.ErrorContains(t, err, "scheme must be http or https")
	})

	t.Run("WithGlobalConfig rejects invalid URL at NewClient time", func(t *testing.T) {
		cfg := &GlobalConfig{Registries: []RegistryEntry{{URL: ""}}}
		_, err := NewClient(WithGlobalConfig(cfg))
		assert.ErrorContains(t, err, "empty url")
	})

	t.Run("WithGlobalConfig rejects non-http scheme at NewClient time", func(t *testing.T) {
		cfg := &GlobalConfig{Registries: []RegistryEntry{{URL: "ftp://example.com"}}}
		_, err := NewClient(WithGlobalConfig(cfg))
		assert.ErrorContains(t, err, "scheme must be http or https")
	})

	t.Run("global replace_defaults with no entries rejected when project also has none", func(t *testing.T) {
		cfg := &GlobalConfig{ReplaceDefaults: true}
		_, err := NewClient(WithGlobalConfig(cfg))
		assert.ErrorContains(t, err, "empty registry list")
	})

	t.Run("project replace_defaults=false overrides global true even with no global entries", func(t *testing.T) {
		cfg := &GlobalConfig{ReplaceDefaults: true}
		c, err := NewClient(WithGlobalConfig(cfg), WithProjectRegistries(nil, boolPtr(false)))
		require.NoError(t, err)
		assert.NotEmpty(t, c.Registries())
	})

	t.Run("global replace_defaults=true with empty global entries accepted when project supplies them", func(t *testing.T) {
		cfg := &GlobalConfig{ReplaceDefaults: true}
		c, err := NewClient(
			WithGlobalConfig(cfg),
			WithProjectRegistries([]RegistryEntry{{URL: "https://proj.example.com"}}, nil),
		)
		require.NoError(t, err)
		regs := c.Registries()
		require.Len(t, regs, 1, "global replace_defaults drops built-in defaults; only project entry remains")
		assert.Equal(t, "https://proj.example.com", regs[0].BaseURL.String())
	})
}
