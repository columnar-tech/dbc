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
	"bytes"
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

func TestDriverSourceProjectConfigRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want dbc.DriverSource
	}{
		{
			name: "explicit registry",
			toml: "[drivers.example]\n[drivers.example.source]\ntype = 'registry'\nurl = 'https://registry.example.test/custom'\n",
			want: dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: "https://registry.example.test/custom"},
		},
		{
			name: "packslip project",
			toml: "[drivers.example]\nversion = '1.2.3'\n[drivers.example.source]\ntype = 'packslip'\nproject = 'github.com/owner/project'\n",
			want: dbc.DriverSource{Type: dbc.DriverSourcePackslip, Project: "github.com/owner/project"},
		},
		{
			name: "relative path",
			toml: "[drivers.example]\n[drivers.example.source]\ntype = 'path'\npath = '../packages/driver.tar.gz'\n",
			want: dbc.DriverSource{Type: dbc.DriverSourcePath, Path: "../packages/driver.tar.gz"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var list DriversList
			require.NoError(t, toml.Unmarshal([]byte(tt.toml), &list))
			require.NoError(t, list.validateSources())
			require.NotNil(t, list.Drivers["example"].Source)
			assert.Equal(t, tt.want, *list.Drivers["example"].Source)

			encoded, err := toml.Marshal(list)
			require.NoError(t, err)
			var roundTrip DriversList
			require.NoError(t, toml.Unmarshal(encoded, &roundTrip))
			require.NoError(t, roundTrip.validateSources())
			require.NotNil(t, roundTrip.Drivers["example"].Source)
			assert.Equal(t, tt.want, *roundTrip.Drivers["example"].Source)
		})
	}
}

func TestDriverSourceProjectConfigValidation(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		wantErr  string
	}{
		{name: "legacy entry has no source", contents: "[drivers]\nexample = {version = '>=1.0.0'}"},
		{name: "missing source type", contents: "[drivers.example.source]\nurl = 'https://registry.example.test'", wantErr: "driver source has no type"},
		{name: "unknown source type", contents: "[drivers.example.source]\ntype = 'git'\nurl = 'https://example.test'", wantErr: `unsupported driver source type "git"`},
		{name: "mutually exclusive fields", contents: "[drivers.example.source]\ntype = 'path'\npath = '../package.tar.gz'\nproject = 'github.com/owner/project'", wantErr: "path source contains fields for another source type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var list DriversList
			err := toml.Unmarshal([]byte(tt.contents), &list)
			if err == nil {
				err = list.validateSources()
			}
			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.Nil(t, list.Drivers["example"].Source)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestPackslipSourceRequiresExactStrictSemVer(t *testing.T) {
	tests := []struct {
		name    string
		version string
		wantErr bool
	}{
		{name: "exact release", version: "1.2.3"},
		{name: "exact prerelease", version: "1.2.3-rc.1"},
		{name: "exact build metadata", version: "1.2.3+build.5"},
		{name: "exact prerelease and build metadata", version: "1.2.3-rc.1+build.5"},
		{name: "explicit equality operator", version: "=1.2.3", wantErr: true},
		{name: "range", version: ">=1.2.3", wantErr: true},
		{name: "latest", version: "latest", wantErr: true},
		{name: "v prefix", version: "v1.2.3", wantErr: true},
		{name: "abbreviated version", version: "1.2", wantErr: true},
		{name: "leading zero", version: "01.2.3", wantErr: true},
		{name: "missing version", version: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contents := "[drivers.example]\n"
			if tt.version != "" {
				contents += "version = '" + tt.version + "'\n"
			}
			contents += "[drivers.example.source]\ntype = 'packslip'\nproject = 'github.com/owner/project'\n"

			var list DriversList
			err := toml.Unmarshal([]byte(contents), &list)
			if err == nil {
				err = list.validateSources()
			}
			if tt.wantErr {
				require.Error(t, err)
				if list.Drivers["example"].Source != nil && list.Drivers["example"].Version != nil {
					require.ErrorContains(t, err, "exact SemVer 2.0.0",
						"a parsed constraint must retain enough spelling for strict validation")
				}
				return
			}
			require.NoError(t, err)
		})
	}

	// Packslip's exact-version requirement must not tighten the legacy source
	// behavior: registry constraints and unconstrained registry entries remain
	// valid.
	for _, contents := range []string{
		"[drivers.example]\nversion = '>=1.2.3'\n",
		"[drivers.example]\n",
	} {
		var list DriversList
		require.NoError(t, toml.Unmarshal([]byte(contents), &list))
		require.NoError(t, list.validateSources())
	}
}

func TestPathSourceRequiresOptionalExactStrictSemVer(t *testing.T) {
	tests := []struct {
		name    string
		version string
		flag    string
		wantErr bool
	}{
		{name: "metadata-derived version"},
		{name: "exact release", version: "1.2.3"},
		{name: "exact prerelease", version: "1.2.3-rc.1"},
		{name: "exact build metadata", version: "1.2.3+build.5"},
		{name: "explicit equality", version: "=1.2.3", wantErr: true},
		{name: "range", version: ">=1.2.3", wantErr: true},
		{name: "v prefix", version: "v1.2.3", wantErr: true},
		{name: "abbreviated", version: "1.2", wantErr: true},
		{name: "leading zero", version: "01.2.3", wantErr: true},
		{name: "prerelease policy", flag: "allow", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contents := "[drivers.example]\n"
			if test.version != "" {
				contents += "version = '" + test.version + "'\n"
			}
			if test.flag != "" {
				contents += "prerelease = '" + test.flag + "'\n"
			}
			contents += "[drivers.example.source]\ntype = 'path'\npath = './example.tgz'\n"
			var list DriversList
			err := toml.Unmarshal([]byte(contents), &list)
			if err == nil {
				err = list.validateSources()
			}
			if test.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
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

func TestMarshalDriverListEmptyTableSection(t *testing.T) {
	// Regression test for go-toml v2.2 → v2.4 upgrade: v2.4 drops the
	// blank line after an empty table section, which changes dbc.toml output.
	//
	// Runs
	// $ dbc init
	// $ dbc add test-driver-1 "test-driver-2>=1.0.0"
	//
	// and asserts on the TOML output
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "dbc.toml")

	{
		m := InitCmd{Path: tomlPath}.GetModel()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out), tea.WithContext(ctx))
		_, err := p.Run()
		require.NoError(t, err)
	}

	{
		m := AddCmd{Path: tomlPath, Driver: []string{"test-driver-1"}}.GetModelCustom(testBaseModel())
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out), tea.WithContext(ctx))
		_, err := p.Run()
		require.NoError(t, err)
	}

	{
		m := AddCmd{Path: tomlPath, Driver: []string{"test-driver-2>=1.0.0"}}.GetModelCustom(testBaseModel())
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		var out bytes.Buffer
		p := tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out), tea.WithContext(ctx))
		_, err := p.Run()
		require.NoError(t, err)
	}

	data, err := os.ReadFile(tomlPath)
	require.NoError(t, err)
	assert.Equal(t, `# dbc driver list
[drivers]
[drivers.test-driver-1]
[drivers.test-driver-2]
version = '>=1.0.0'
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

	t.Run("both invalid configs fail closed", func(t *testing.T) {
		invalid := DriversList{Registries: []dbc.RegistryEntry{{URL: "ftp://registry.example.com"}}}
		_, errA := effectiveRegistryKeys(invalid)
		_, errB := effectiveRegistryKeys(invalid)
		require.Error(t, errA)
		require.Error(t, errB)
		assert.True(t, registriesChanged(invalid, invalid),
			"two unanalyzable registry configs must be treated as changed")
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

	t.Run("escaped separator differs from literal separator", func(t *testing.T) {
		a := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://r.example.com/a%2Fb"}}}
		b := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://r.example.com/a/b"}}}
		assert.True(t, registriesChanged(a, b))
	})

	t.Run("empty force query is significant", func(t *testing.T) {
		a := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://r.example.com?"}}}
		b := DriversList{Registries: []dbc.RegistryEntry{{URL: "https://r.example.com"}}}
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

func TestDriverSourceIdentityComparison(t *testing.T) {
	tests := []struct {
		name string
		a    *dbc.DriverSource
		b    *dbc.DriverSource
		want bool
	}{
		{name: "both omitted sources match", want: true},
		{name: "omitted source remains distinct from explicit registry", b: &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: "https://registry.example.test"}},
		{name: "registry canonical equivalences match", a: &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: "HTTPS://REGISTRY.EXAMPLE.TEST/#one"}, b: &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: "https://registry.example.test"}, want: true},
		{name: "registry escaped path distinction is preserved", a: &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: "https://registry.example.test/a%2Fb"}, b: &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: "https://registry.example.test/a/b"}},
		{name: "registry force query distinction is preserved", a: &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: "https://registry.example.test?"}, b: &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: "https://registry.example.test"}},
		{name: "packslip owner and repo case normalize", a: &dbc.DriverSource{Type: dbc.DriverSourcePackslip, Project: "GitHub.com/Example/Driver/Tools"}, b: &dbc.DriverSource{Type: dbc.DriverSourcePackslip, Project: "github.com/example/driver/Tools"}, want: true},
		{name: "packslip tool path case is preserved", a: &dbc.DriverSource{Type: dbc.DriverSourcePackslip, Project: "github.com/example/driver/Tools"}, b: &dbc.DriverSource{Type: dbc.DriverSourcePackslip, Project: "github.com/example/driver/tools"}},
		{name: "path is exact string identity", a: &dbc.DriverSource{Type: dbc.DriverSourcePath, Path: "./packages/../driver.tgz"}, b: &dbc.DriverSource{Type: dbc.DriverSourcePath, Path: "./driver.tgz"}},
		{name: "invalid source does not fall back to raw comparison", a: &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: "not a URL"}, b: &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: "not a URL"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, sameDriverSourceIdentity(test.a, test.b))
		})
	}
}

func TestLockSourceIdentityComparison(t *testing.T) {
	tests := []struct {
		name string
		a    lockSource
		b    lockSource
		want bool
	}{
		{name: "registry URL canonical equivalences match", a: lockSource{Type: "registry", URL: "HTTPS://REGISTRY.EXAMPLE.TEST/a/#one"}, b: lockSource{Type: "registry", URL: "https://registry.example.test/a"}, want: true},
		{name: "escaped separator remains distinct", a: lockSource{Type: "registry", URL: "https://registry.example.test/a%2Fb"}, b: lockSource{Type: "registry", URL: "https://registry.example.test/a/b"}},
		{name: "packslip owner and repo case normalize", a: lockSource{Type: "packslip", Project: "GitHub.com/Example/Driver/Tools"}, b: lockSource{Type: "packslip", Project: "github.com/example/driver/Tools"}, want: true},
		{name: "path aliases stay distinct", a: lockSource{Type: "path", Path: "./packages/../driver.tgz"}, b: lockSource{Type: "path", Path: "./driver.tgz"}},
		{name: "malformed identity has no raw fallback", a: lockSource{Type: "registry", URL: "not a URL"}, b: lockSource{Type: "registry", URL: "not a URL"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, sameLockSourceIdentity(test.a, test.b))
		})
	}
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
