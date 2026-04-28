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

func TestDriversListRegistries(t *testing.T) {
	t.Run("existing dbc.toml without registries still decodes (backward compat)", func(t *testing.T) {
		content := "[drivers]\n[drivers.clickhouse]\nversion = '>=1.0.0'"
		var list DriversList
		require.NoError(t, toml.Unmarshal([]byte(content), &list))
		assert.Len(t, list.Drivers, 1)
		assert.Empty(t, list.Registries)
		assert.False(t, list.ReplaceDefaults)
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
}
