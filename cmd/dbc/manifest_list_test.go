// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

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

func TestUnmarshalManifestList(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		expected []dbc.PkgInfo
		err      error
	}{
		{"basic", "[drivers]\nbigquery = {version = '1.6.0'}\nflightsql = {version = '1.6.0'}", []dbc.PkgInfo{
			{Driver: dbc.Driver{Path: "bigquery"}, Version: semver.MustParse("1.6.0")},
			{Driver: dbc.Driver{Path: "flightsql"}, Version: semver.MustParse("1.6.0")},
		}, nil},
		{"less", "[drivers]\nbigquery = {version = '<1.6.0'}\nflightsql = {version = '<=1.6.0'}", []dbc.PkgInfo{
			{Driver: dbc.Driver{Path: "bigquery"}, Version: semver.MustParse("1.5.0")},
			{Driver: dbc.Driver{Path: "flightsql"}, Version: semver.MustParse("1.6.0")},
		}, nil},
		{"greater", "[drivers]\nbigquery = {version = '>1.5.0'}\nflightsql = {version = '>=1.6.0'}", []dbc.PkgInfo{
			{Driver: dbc.Driver{Path: "bigquery"}, Version: semver.MustParse("1.6.0")},
			{Driver: dbc.Driver{Path: "flightsql"}, Version: semver.MustParse("1.6.0")},
		}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			manifestPath := filepath.Join(tmpdir, "manifest.txt")
			require.NoError(t, os.WriteFile(manifestPath, []byte(tt.contents), 0644))

			pkgs, err := GetDriverList(manifestPath, platformTuple)
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

func TestMarshalManifestList(t *testing.T) {
	data, err := toml.Marshal(ManifestList{
		Drivers: map[string]driverSpec{
			"bigquery":  {Version: must(semver.NewConstraint(">=1.6.0"))},
			"flightsql": {Version: must(semver.NewConstraint(">=1.6.0"))},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, `# dbc driver list
[drivers]
[drivers.bigquery]
version = '>=1.6.0'

[drivers.flightsql]
version = '>=1.6.0'
`, string(data))
}
