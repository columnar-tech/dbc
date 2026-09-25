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
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/go-faster/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDriverGetPackagesReturnsExactReleaseInPlatformOrder(t *testing.T) {
	const hash = "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var fixture struct {
		Drivers []Driver `yaml:"drivers"`
	}
	index := `drivers:
  - name: Example Driver
    description: test fixture
    license: MIT
    path: example-driver
    pkginfo:
      - version: 1.2.3
        packages:
          - platform: windows_amd64
            url: windows.tar.gz
            hash: ` + hash + `
          - platform: linux_amd64
            url: linux.tar.gz
            hash: ` + hash + `
      - version: 1.1.0
        packages:
          - platform: linux_amd64
            url: old.tar.gz
            hash: ` + hash + `
`
	require.NoError(t, yaml.NewDecoder(strings.NewReader(index)).Decode(&fixture))
	require.Len(t, fixture.Drivers, 1)
	driver := fixture.Drivers[0]
	driver.Registry = &Registry{BaseURL: mustParseURL("https://registry.example.test")}

	packages, err := driver.GetPackages(semver.MustParse("1.2.3"))
	require.NoError(t, err)
	require.Len(t, packages, 2)
	assert.Equal(t, []string{"linux_amd64", "windows_amd64"}, []string{packages[0].PlatformTuple, packages[1].PlatformTuple})
	assert.Equal(t, "https://registry.example.test/linux.tar.gz", packages[0].Path.String())
	assert.Equal(t, hash, packages[0].ArtifactHash)
	assert.Nil(t, packages[0].ArtifactSize)

	_, err = driver.GetPackages(nil)
	require.ErrorContains(t, err, "exact registry version is required")
	_, err = driver.GetPackages(semver.MustParse("9.9.9"))
	require.ErrorContains(t, err, "version 9.9.9 not found")
}
