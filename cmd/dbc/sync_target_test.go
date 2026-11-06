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

func TestPlanRejectsDuplicateCanonicalTargets(t *testing.T) {
	index := `drivers:
  - name: Alpha
    path: alpha
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: linux_amd64
            hash: sha256:` + strings.Repeat("a", 64) + `
          - platform: linux_x86_64
            hash: sha256:` + strings.Repeat("a", 64) + "\n"
	planner := syncPlanner{
		target:     "linux_amd64",
		registries: func() ([]dbc.Registry, error) { return []dbc.Registry{testRegistry}, nil },
		index:      func() ([]dbc.Driver, error) { return decodeTestIndex(index) },
	}
	_, err := planner.resolve("alpha", driverSpec{}, nil)
	require.ErrorContains(t, err, "duplicate artifact target")
}
