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
	"github.com/columnar-tech/dbc/internal/resolution"
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

func TestResolveRegistryPackageURLStateTable(t *testing.T) {
	types := []struct {
		name string
		pkg  registryPackage
	}{
		{name: "absolute", pkg: registryPackage{URL: "https://packages.example.test/driver.tar.gz"}},
		{name: "relative", pkg: registryPackage{URL: "driver.tar.gz"}},
		{name: "implicit", pkg: registryPackage{PlatformTuple: "linux_amd64"}},
	}
	registries := []struct {
		name     string
		registry *Registry
		valid    bool
	}{
		{name: "nil"},
		{name: "empty", registry: &Registry{}},
		{name: "valid", registry: &Registry{BaseURL: mustParseURL("https://registry.example.test")}, valid: true},
	}
	versions := []struct {
		name    string
		version *semver.Version
	}{
		{name: "nil"},
		{name: "present", version: semver.MustParse("1.2.3")},
	}

	for _, urlType := range types {
		for _, registry := range registries {
			for _, version := range versions {
				name := urlType.name + "/registry=" + registry.name + "/version=" + version.name
				t.Run(name, func(t *testing.T) {
					wantSuccess := urlType.name == "absolute" ||
						(urlType.name == "relative" && registry.valid) ||
						(urlType.name == "implicit" && registry.valid && version.version != nil)
					driver := Driver{Title: "Example Driver", Path: "example-driver", Registry: registry.registry}
					uri, err := resolveRegistryPackageURL(driver, version.version, urlType.pkg)
					if !wantSuccess {
						require.Error(t, err)
						return
					}
					require.NoError(t, err)
					wantURL := "https://packages.example.test/driver.tar.gz"
					if urlType.name == "relative" {
						wantURL = "https://registry.example.test/driver.tar.gz"
					}
					if urlType.name == "implicit" {
						wantURL = "https://registry.example.test/example-driver/1.2.3/example-driver_linux_amd64-1.2.3.tar.gz"
					}
					assert.Equal(t, wantURL, uri.String())
				})
			}
		}
	}
}

func TestResolvedRegistryReleaseRequiresSourceIdentity(t *testing.T) {
	absoluteRelease := pkginfo{
		Version: semver.MustParse("1.2.3"),
		Packages: []registryPackage{{
			PlatformTuple: "linux_amd64",
			URL:           "https://packages.example.test/driver.tar.gz",
		}},
	}
	tests := []struct {
		name           string
		driver         Driver
		wantErr        string
		missingVersion bool
	}{
		{name: "missing driver ID", driver: Driver{Title: "Example Driver", Registry: &Registry{BaseURL: mustParseURL("https://registry.example.test")}}, wantErr: "driver ID is empty"},
		{name: "missing registry", driver: Driver{Path: "example-driver"}, wantErr: "registry BaseURL is missing"},
		{name: "missing registry BaseURL", driver: Driver{Path: "example-driver", Registry: &Registry{}}, wantErr: "registry BaseURL is missing"},
		{name: "invalid registry identity", driver: Driver{Path: "example-driver", Registry: &Registry{BaseURL: mustParseURL("file:///tmp/registry")}}, wantErr: "absolute HTTP(S) URL with a host"},
		{name: "missing version", driver: Driver{Path: "example-driver", Registry: &Registry{BaseURL: mustParseURL("https://registry.example.test")}}, wantErr: "release has no version", missingVersion: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := absoluteRelease
			if tt.missingVersion {
				release.Version = nil
			}
			_, err := release.resolvedRelease(tt.driver)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestResolvedRegistryReleaseKeepsSourceAndArtifactHostsSeparate(t *testing.T) {
	release := pkginfo{
		Version: semver.MustParse("1.2.3"),
		Packages: []registryPackage{{
			PlatformTuple: "linux_amd64",
			URL:           "https://packages.example.test/driver.tar.gz",
		}},
	}
	driver := Driver{Path: "example-driver", Registry: &Registry{BaseURL: mustParseURL("https://registry.example.test")}}

	resolved, err := release.resolvedRelease(driver)
	require.NoError(t, err)
	assert.Equal(t, "https://registry.example.test", resolved.Source.Reference)
	require.Len(t, resolved.Artifacts, 1)
	assert.Equal(t, "https://packages.example.test/driver.tar.gz", resolved.Artifacts[0].URL)
}

func TestResolvedRegistryReleaseSourceIdentityIncludesRegistry(t *testing.T) {
	release := pkginfo{
		Version:  semver.MustParse("1.2.3"),
		Packages: []registryPackage{{PlatformTuple: "linux_amd64", URL: "https://packages.example.test/driver.tar.gz"}},
	}
	first, err := release.resolvedRelease(Driver{Path: "example-driver", Registry: &Registry{BaseURL: mustParseURL("https://registry-one.example.test")}})
	require.NoError(t, err)
	second, err := release.resolvedRelease(Driver{Path: "example-driver", Registry: &Registry{BaseURL: mustParseURL("https://registry-two.example.test")}})
	require.NoError(t, err)
	assert.NotEqual(t, first.Source.Reference, second.Source.Reference)
}

func TestResolvedRegistryReleaseMatchesGetPackageURLs(t *testing.T) {
	version := semver.MustParse("1.2.3")
	release := pkginfo{
		Version: version,
		Packages: []registryPackage{
			{PlatformTuple: "linux_amd64", URL: "packages/linux.tar.gz"},
			{PlatformTuple: "windows_amd64"},
		},
	}
	driver := Driver{
		Path:     "example-driver",
		Registry: &Registry{BaseURL: mustParseURL("https://registry.example.test")},
		PkgInfo:  []pkginfo{release},
	}
	resolved, err := release.resolvedRelease(driver)
	require.NoError(t, err)

	for _, rawPackage := range release.Packages {
		target, err := resolution.TargetFromPlatformTuple(rawPackage.PlatformTuple)
		require.NoError(t, err)
		var artifact resolution.Artifact
		found := false
		for _, candidate := range resolved.Artifacts {
			if candidate.Target == target {
				artifact = candidate
				found = true
				break
			}
		}
		require.True(t, found, "resolved release should contain the canonical target for %s", rawPackage.PlatformTuple)
		pkg, err := driver.GetPackage(version, rawPackage.PlatformTuple, false)
		require.NoError(t, err)
		require.NotNil(t, pkg.Path)
		assert.Equal(t, pkg.Path.String(), artifact.URL)
	}
}

func TestRegistryTupleAliasesCanonicalizeWithoutChangingImplicitAssetURL(t *testing.T) {
	version := semver.MustParse("1.2.3")
	release := pkginfo{
		Version:  version,
		Packages: []registryPackage{{PlatformTuple: "linux_x86_64"}},
	}
	driver := Driver{
		Path:     "example-driver",
		Registry: &Registry{BaseURL: mustParseURL("https://registry.example.test")},
	}
	resolved, err := release.resolvedRelease(driver)
	require.NoError(t, err)
	require.Len(t, resolved.Artifacts, 1)
	assert.Equal(t, resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, resolved.Artifacts[0].Target)
	assert.Equal(t, "https://registry.example.test/example-driver/1.2.3/example-driver_linux_x86_64-1.2.3.tar.gz", resolved.Artifacts[0].URL,
		"implicit asset filenames retain the raw registry tuple")
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
