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

package main

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalDriverList(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		expected []dbc.PkgInfo
		err      error
	}{
		{"basic", "[drivers]\nflightsql = {version = '1.8.0'}", []dbc.PkgInfo{
			{Driver: dbc.Driver{Path: "flightsql"}, Version: semver.MustParse("1.8.0")},
		}, nil},
		{"less", "[drivers]\nflightsql = {version = '<=1.8.0'}", []dbc.PkgInfo{
			{Driver: dbc.Driver{Path: "flightsql"}, Version: semver.MustParse("1.8.0")},
		}, nil},
		{"greater", "[drivers]\nflightsql = {version = '>=1.8.0, <=1.10.0'}", []dbc.PkgInfo{
			{Driver: dbc.Driver{Path: "flightsql"}, Version: semver.MustParse("1.10.0")},
		}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			driverListPath := filepath.Join(tmpdir, "dbc.toml")
			require.NoError(t, os.WriteFile(driverListPath, []byte(tt.contents), 0644))

			pkgs, err := GetDriverList(driverListPath)
			if tt.err != nil {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.err.Error())
				return
			}

			require.NoError(t, err)
			assert.Len(t, pkgs, len(tt.expected))

			slices.SortFunc(pkgs, func(a, b dbc.PkgInfo) int {
				return cmp.Compare(a.Driver.Path, b.Driver.Path)
			})
			slices.SortFunc(tt.expected, func(a, b dbc.PkgInfo) int {
				return cmp.Compare(a.Driver.Path, b.Driver.Path)
			})

			for i, pkg := range pkgs {
				assert.Equal(t, tt.expected[i].Driver.Path, pkg.Driver.Path)
				assert.Truef(t, tt.expected[i].Version.Equal(pkg.Version), "expected %s to equal %s", tt.expected[i].Version, pkg.Version)
			}
		})
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestMarshalDriverManifestList(t *testing.T) {
	data, err := toml.Marshal(DriversList{
		Drivers: map[string]driverSpec{
			"flightsql": {Version: must(semver.NewConstraint(">=1.6.0"))},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, `# dbc driver list
[drivers]
[drivers.flightsql]
version = '>=1.6.0'
`, string(data))
}

func TestRegistriesChanged(t *testing.T) {
	bp := func(b bool) *bool { return &b }

	t.Run("identical lists compare equal", func(t *testing.T) {
		a := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://a.example.com"}}}
		b := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://a.example.com"}}}
		assert.False(t, registriesChanged(a, b))
	})

	t.Run("different URL compares unequal", func(t *testing.T) {
		a := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://a.example.com"}}}
		b := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://b.example.com"}}}
		assert.True(t, registriesChanged(a, b))
	})

	t.Run("name-only changes are ignored (display-only field)", func(t *testing.T) {
		a := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://a.example.com", Name: "prod"}}}
		b := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://a.example.com", Name: "production"}}}
		assert.False(t, registriesChanged(a, b),
			"renaming a registry must not trigger a false-positive config-drift abort")
	})

	t.Run("trailing slash normalization", func(t *testing.T) {
		a := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://a.example.com/"}}}
		b := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://a.example.com"}}}
		assert.False(t, registriesChanged(a, b))
	})

	t.Run("case-insensitive host", func(t *testing.T) {
		a := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://A.Example.COM"}}}
		b := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://a.example.com"}}}
		assert.False(t, registriesChanged(a, b))
	})

	t.Run("fragment-only changes are ignored (not sent on HTTP requests)", func(t *testing.T) {
		a := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://r.example.com/#a"}}}
		b := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://r.example.com/#b"}}}
		assert.False(t, registriesChanged(a, b),
			"fragments don't affect registry fetches; fragment-only edits must not abort dbc add")
	})

	t.Run("query string changes ARE significant", func(t *testing.T) {
		a := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://r.example.com/?tenant=a"}}}
		b := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://r.example.com/?tenant=b"}}}
		assert.True(t, registriesChanged(a, b),
			"tenant-selector query changes change the effective endpoint and must not be normalized away")
	})

	t.Run("userinfo changes ARE significant", func(t *testing.T) {
		a := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://u1:p@r.example.com"}}}
		b := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://u2:p@r.example.com"}}}
		assert.True(t, registriesChanged(a, b),
			"userinfo changes change the authenticated identity and must not be normalized away")
	})

	t.Run("path segment changes ARE significant", func(t *testing.T) {
		a := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://r.example.com/a"}}}
		b := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://r.example.com/b"}}}
		assert.True(t, registriesChanged(a, b))
	})

	t.Run("replace_defaults tri-state differences compare unequal", func(t *testing.T) {
		a := DriversList{ReplaceDefaults: bp(true), Registries: []dbc.RegistryEntry{{URL: "https://r.example.com"}}}
		b := DriversList{ReplaceDefaults: bp(false), Registries: []dbc.RegistryEntry{{URL: "https://r.example.com"}}}
		assert.True(t, registriesChanged(a, b),
			"replace_defaults=true drops built-in defaults; the merged sets differ")
	})

	t.Run("nil vs explicit false replace_defaults compare equal (no-op flip)", func(t *testing.T) {
		// When no global config is loaded, nil and &false both mean
		// "keep defaults" — the merged registry set is identical.
		a := DriversList{ReplaceDefaults: nil, Registries: []dbc.RegistryEntry{{URL: "https://r.example.com"}}}
		b := DriversList{ReplaceDefaults: bp(false), Registries: []dbc.RegistryEntry{{URL: "https://r.example.com"}}}
		assert.False(t, registriesChanged(a, b),
			"flipping unset→explicit-false without a global override doesn't change the merged set")
	})

	t.Run("exact duplicate entries collapse (mergeRegistries dedupes)", func(t *testing.T) {
		// Adding a duplicate registry entry is a no-op because
		// mergeRegistries dedupes. The effective resolution is unchanged.
		a := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://r.example.com"}}}
		b := DriversList{Registries: []dbc.RegistryEntry{
			{URL: "https://r.example.com"},
			{URL: "https://r.example.com"},
		}}
		assert.False(t, registriesChanged(a, b),
			"adding an exact duplicate registry entry doesn't change the merged set")
	})

	t.Run("name-only duplicate rename is a no-op", func(t *testing.T) {
		// Same URLs, different names — merge uses first-wins for Name,
		// and registriesChanged compares only URLs, so this is equal.
		a := DriversList{Registries: []dbc.RegistryEntry{
			{URL: "https://r.example.com", Name: "a"},
			{URL: "https://r.example.com", Name: "b"},
		}}
		b := DriversList{Registries: []dbc.RegistryEntry{
			{URL: "https://r.example.com", Name: "c"},
		}}
		assert.False(t, registriesChanged(a, b))
	})
}

func TestDriversListRegistries(t *testing.T) {
	t.Run("existing dbc.toml without registries still decodes (backward compat)", func(t *testing.T) {
		content := "[drivers]\n[drivers.clickhouse]\nversion = '>=1.0.0'"
		var list DriversList
		require.NoError(t, toml.Unmarshal([]byte(content), &list))
		assert.Len(t, list.Drivers, 1)
		assert.Empty(t, list.Registries)
		assert.Nil(t, list.ReplaceDefaults)
	})

	t.Run("dbc.toml with drivers AND registries decodes both", func(t *testing.T) {
		content := "[drivers]\n[drivers.snowflake]\nversion = '>=1.0.0'\n\n[[registries]]\nurl = \"https://custom.example.com\"\nname = \"Custom\""
		var list DriversList
		require.NoError(t, toml.Unmarshal([]byte(content), &list))
		assert.Len(t, list.Drivers, 1)
		require.Len(t, list.Registries, 1)
		assert.Equal(t, "https://custom.example.com", list.Registries[0].URL)
		assert.Equal(t, "Custom", list.Registries[0].Name)
	})

	t.Run("replace_defaults = false is preserved as explicit false (tri-state)", func(t *testing.T) {
		content := "replace_defaults = false\n[drivers]"
		var list DriversList
		require.NoError(t, toml.Unmarshal([]byte(content), &list))
		require.NotNil(t, list.ReplaceDefaults)
		assert.False(t, *list.ReplaceDefaults)
	})

	t.Run("replace_defaults = true parses as explicit true", func(t *testing.T) {
		content := "replace_defaults = true\n[[registries]]\nurl = \"https://a.example.com\"\n[drivers]"
		var list DriversList
		require.NoError(t, toml.Unmarshal([]byte(content), &list))
		require.NotNil(t, list.ReplaceDefaults)
		assert.True(t, *list.ReplaceDefaults)
	})
}
