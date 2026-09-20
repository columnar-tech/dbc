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
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/go-faster/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryArtifactMetadataFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/registry-index-artifact-metadata.yaml")
	require.NoError(t, err)

	var index struct {
		Drivers []Driver `yaml:"drivers"`
	}
	require.NoError(t, yaml.NewDecoder(strings.NewReader(string(data))).Decode(&index))
	require.Len(t, index.Drivers, 1)
	require.NoError(t, validateRegistryMetadata(index.Drivers))

	driver := index.Drivers[0]
	driver.Registry = &Registry{BaseURL: mustParseURL("https://registry.example.test")}
	pkg, err := driver.GetPackage(nil, "linux_amd64", false)
	require.NoError(t, err)
	assert.Equal(t, "sha256:0000000000000000000000000000000000000000000000000000000000000000", pkg.ArtifactHash)
	require.NotNil(t, pkg.ArtifactSize)
	assert.EqualValues(t, 12345, *pkg.ArtifactSize)

	// The fixture intentionally describes only one target platform.
	_, err = driver.GetPackage(nil, "windows_amd64", false)
	require.Error(t, err)
}

func TestRegistryArtifactMetadataOptional(t *testing.T) {
	pkg := decodeRegistryPackage(t, "platform: linux_amd64\n")
	artifact, err := pkg.resolveArtifact()
	require.NoError(t, err)
	assert.Empty(t, artifact.Hash)
	assert.Nil(t, artifact.Size)

	pkg = decodeRegistryPackage(t, "platform: linux_amd64\nsize: 0\n")
	artifact, err = pkg.resolveArtifact()
	require.NoError(t, err)
	require.NotNil(t, artifact.Size)
	assert.Zero(t, *artifact.Size)
}

func TestResolvedRegistryReleaseFillsURLsForEveryPlatform(t *testing.T) {
	var release pkginfo
	require.NoError(t, yaml.NewDecoder(strings.NewReader(`
version: v1.2.3
packages:
  - platform: linux_amd64
  - platform: windows_amd64
`)).Decode(&release))
	driver := Driver{
		Path:     "example-driver",
		Registry: &Registry{BaseURL: mustParseURL("https://registry.example.test")},
	}

	resolved, err := release.resolvedRelease(driver)
	require.NoError(t, err)
	require.Len(t, resolved.Artifacts, 2)
	assert.Equal(t, "https://registry.example.test/example-driver/1.2.3/example-driver_linux_amd64-1.2.3.tar.gz", resolved.Artifacts[0].URL)
	assert.Equal(t, "https://registry.example.test/example-driver/1.2.3/example-driver_windows_amd64-1.2.3.tar.gz", resolved.Artifacts[1].URL)
}

func TestResolveRegistryPackageURLWithoutBaseURL(t *testing.T) {
	driver := Driver{Title: "Example Driver", Registry: &Registry{}}

	t.Run("absolute URL", func(t *testing.T) {
		uri, err := resolveRegistryPackageURL(driver, nil, registryPackage{
			URL: "https://packages.example.test/driver.tar.gz",
		})
		require.NoError(t, err)
		assert.Equal(t, "https://packages.example.test/driver.tar.gz", uri.String())
	})

	t.Run("relative URL", func(t *testing.T) {
		_, err := resolveRegistryPackageURL(driver, semver.MustParse("1.2.3"), registryPackage{
			URL: "driver.tar.gz",
		})
		require.ErrorContains(t, err, "no registry URL")
	})

	t.Run("implicit URL", func(t *testing.T) {
		_, err := resolveRegistryPackageURL(driver, semver.MustParse("1.2.3"), registryPackage{
			PlatformTuple: "linux_amd64",
		})
		require.ErrorContains(t, err, "no registry URL")
	})
}

func TestRegistryArtifactMetadataRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		want     string
	}{
		{name: "unknown algorithm", metadata: "hash: sha512:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", want: "unsupported artifact hash algorithm"},
		{name: "short digest", metadata: "hash: sha256:abcd\n", want: "64 lowercase hexadecimal"},
		{name: "uppercase digest", metadata: "hash: sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n", want: "64 lowercase hexadecimal"},
		{name: "missing algorithm", metadata: "hash: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n", want: "include an algorithm prefix"},
		{name: "non-string digest", metadata: "hash: 123\n", want: "hash must be a string"},
		{name: "empty digest", metadata: "hash: \"\"\n", want: "hash must not be empty"},
		{name: "null digest", metadata: "hash: null\n", want: "hash must be a string"},
		{name: "non-integer size", metadata: "size: 1.5\n", want: "size must be an integer"},
		{name: "string size", metadata: "size: \"123\"\n", want: "size must be an integer"},
		{name: "null size", metadata: "size: null\n", want: "size must be an integer"},
		{name: "negative size", metadata: "size: -1\n", want: "must not be negative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg := decodeRegistryPackage(t, "platform: linux_amd64\n"+tt.metadata)
			_, err := pkg.resolveArtifact()
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func decodeRegistryPackage(t *testing.T, data string) registryPackage {
	t.Helper()
	var pkg registryPackage
	require.NoError(t, yaml.NewDecoder(strings.NewReader(data)).Decode(&pkg))
	return pkg
}
