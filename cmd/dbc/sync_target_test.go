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
	"fmt"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanRetainsPlatformTupleForArchiveURLs(t *testing.T) {
	for _, tc := range []struct {
		tuple, hostTuple string
		target           resolution.Target
	}{
		{"darwin_aarch64", "macos_arm64", resolution.Target{OS: "macos", Arch: "arm64"}},
		{"linux_x86_64_musl_v3", "linux_amd64_musl_v3", resolution.Target{OS: "linux", Arch: "amd64", LibC: "musl", Variant: "v3"}},
		{"linux_amd64", "linux_amd64", resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"}},
	} {
		t.Run(tc.tuple, func(t *testing.T) {
			index := fmt.Sprintf(`drivers:
  - name: Alpha
    path: alpha
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: %s
            hash: sha256:%s
`, tc.tuple, strings.Repeat("a", 64))
			planner := syncPlanner{
				target:     tc.tuple,
				registries: func() ([]dbc.Registry, error) { return []dbc.Registry{testRegistry}, nil },
				index:      func() ([]dbc.Driver, error) { return decodeTestIndex(index) },
			}
			planned, err := planner.resolve("alpha", driverSpec{}, nil)
			require.NoError(t, err)
			require.Len(t, planned.Entry.Artifacts, 1)
			assert.Equal(t, tc.target, planned.Entry.Artifacts[0].Target)
			assert.Equal(t, tc.tuple, planned.Package.PlatformTuple)
			assert.Contains(t, planned.Package.Path.String(), "_"+tc.tuple+"-1.0.0.tar.gz")

			// Matching a canonical target must use its recorded location, not
			// guess the registry's original platform spelling from the target.
			planner.target = tc.hostTuple
			planner.index = func() ([]dbc.Driver, error) { return nil, fmt.Errorf("index must not be fetched") }
			replayed, err := planner.fromLock("alpha", planned.Entry, planned.Package.Version)
			require.NoError(t, err)
			assert.Equal(t, planned.Package.Path.String(), replayed.Package.Path.String())
			assert.Equal(t, tc.hostTuple, replayed.Package.PlatformTuple)
		})
	}
}

func TestPlanSkipsAliasesOfCurrentTargetInEitherOrder(t *testing.T) {
	for _, platforms := range [][2]string{{"linux_amd64", "linux_x86_64"}, {"linux_x86_64", "linux_amd64"}} {
		t.Run(platforms[0]+" then "+platforms[1], func(t *testing.T) {
			index := fmt.Sprintf(`drivers:
  - name: Alpha
    path: alpha
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: %s
            hash: sha256:%s
          - platform: %s
            hash: sha256:%s
`, platforms[0], strings.Repeat("a", 64), platforms[1], strings.Repeat("a", 64))
			planner := targetTestPlanner(platforms[1], index)
			planned, err := planner.resolve("alpha", driverSpec{}, nil)
			require.NoError(t, err)
			require.Len(t, planned.Entry.Artifacts, 1)
			assert.Equal(t, resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, planned.Entry.Artifacts[0].Target)
		})
	}
}

func TestPlanExcludesAmbiguousSecondaryCanonicalTargetInEitherOrder(t *testing.T) {
	for _, platforms := range [][2]string{{"macos_arm64", "darwin_aarch64"}, {"darwin_aarch64", "macos_arm64"}} {
		t.Run(platforms[0]+" then "+platforms[1], func(t *testing.T) {
			index := fmt.Sprintf(`drivers:
  - name: Alpha
    path: alpha
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            hash: sha256:%s
          - platform: %s
            hash: sha256:%s
          - platform: %s
            hash: sha256:%s
`, strings.Repeat("a", 64), platforms[0], strings.Repeat("b", 64), platforms[1], strings.Repeat("c", 64))
			planner := targetTestPlanner("linux_amd64", index)
			planned, err := planner.resolve("alpha", driverSpec{}, nil)
			require.NoError(t, err)
			require.Len(t, planned.Entry.Artifacts, 1)
			assert.Equal(t, resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, planned.Entry.Artifacts[0].Target)
		})
	}
}

func TestPlanSkipsUnrecordableOrConflictingSecondaryArtifacts(t *testing.T) {
	for name, extra := range map[string]string{
		"query signature": `          - platform: macos_arm64
            url: https://cdn.example.com/archive.tar.gz?signature=secret
            hash: sha256:` + strings.Repeat("b", 64),
		"relative query signature": `          - platform: macos_arm64
            url: archive.tar.gz?signature=secret
            hash: sha256:` + strings.Repeat("b", 64),
		"current location conflict": `          - platform: macos_arm64
            url: shared.tar.gz
            hash: sha256:` + strings.Repeat("b", 64),
		"secondary location conflict": `          - platform: macos_arm64
            url: shared.tar.gz
            hash: sha256:` + strings.Repeat("b", 64) + `
          - platform: windows_amd64
            url: shared.tar.gz
            hash: sha256:` + strings.Repeat("c", 64),
	} {
		t.Run(name, func(t *testing.T) {
			currentURL := "linux.tar.gz"
			if name == "current location conflict" {
				currentURL = "shared.tar.gz"
			}
			index := fmt.Sprintf(`drivers:
  - name: Alpha
    path: alpha
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: %s
            hash: sha256:%s
%s
`, currentURL, strings.Repeat("a", 64), extra)
			planner := targetTestPlanner("linux_amd64", index)
			planned, err := planner.resolve("alpha", driverSpec{}, nil)
			require.NoError(t, err)
			require.Len(t, planned.Entry.Artifacts, 1)
			assert.Equal(t, resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, planned.Entry.Artifacts[0].Target)
		})
	}
}

func TestPlanRejectsUnrecordableCurrentRelativeURL(t *testing.T) {
	index := `drivers:
  - name: Alpha
    path: alpha
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: archive.tar.gz?signature=secret
            hash: sha256:` + strings.Repeat("a", 64) + "\n"
	planner := targetTestPlanner("linux_amd64", index)
	_, err := planner.resolve("alpha", driverSpec{}, nil)
	require.ErrorContains(t, err, "unrecordable relative URL")
	assert.NotContains(t, err.Error(), "secret")
}

func TestPlanHandlesSecondaryLocationMatchingCurrentArtifact(t *testing.T) {
	for _, tc := range []struct {
		name        string
		currentHash string
		wantCount   int
	}{
		{name: "matching known hash", currentHash: strings.Repeat("a", 64), wantCount: 2},
		{name: "unknown current hash", wantCount: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			currentHashField := ""
			if tc.currentHash != "" {
				currentHashField = "            hash: sha256:" + tc.currentHash + "\n"
			}
			index := fmt.Sprintf(`drivers:
  - name: Alpha
    path: alpha
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            url: shared.tar.gz
%s          - platform: macos_arm64
            url: shared.tar.gz
            hash: sha256:%s
`, currentHashField, strings.Repeat("a", 64))
			planner := targetTestPlanner("linux_amd64", index)
			planned, err := planner.resolve("alpha", driverSpec{}, nil)
			require.NoError(t, err)
			require.Len(t, planned.Entry.Artifacts, tc.wantCount)
		})
	}
}

func targetTestPlanner(target, index string) syncPlanner {
	return syncPlanner{
		target:     target,
		registries: func() ([]dbc.Registry, error) { return []dbc.Registry{testRegistry}, nil },
		index:      func() ([]dbc.Driver, error) { return decodeTestIndex(index) },
	}
}
