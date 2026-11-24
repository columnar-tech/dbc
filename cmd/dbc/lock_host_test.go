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
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hostlessPlanFixture is a plan fixture for a host whose platform no version
// in planIndexYAML has a build for, unless a test changes the target.
func hostlessPlanFixture(t *testing.T, target string, hostOptional bool) *planFixture {
	f := newPlanFixture(t)
	f.planner.target = target
	f.planner.hostOptional = hostOptional
	return f
}

// TestPlanLocksVersionWithoutHostBuild pins that dbc lock keeps the version
// it selects without regard to platforms when the host has no build for it,
// and records only the targets with a published hash.
func TestPlanLocksVersionWithoutHostBuild(t *testing.T) {
	f := hostlessPlanFixture(t, "macos_amd64", true)
	planned, warnings, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), parsedLockFile{})
	require.NoError(t, err)
	require.Len(t, planned, 1)
	assert.Equal(t, []string{"warning: driver alpha 1.1.0 has no build for this platform (macos_amd64); dbc sync will fail here"}, warnings)

	pd := planned[0]
	assert.False(t, pd.hasHostPackage())
	assert.Equal(t, "1.1.0", pd.Package.Version.String(), "no older version with a host build is chosen")
	assert.Equal(t, "1.1.0", pd.Entry.Version)
	// windows_amd64 publishes no hash and is not downloaded, so it is left out.
	assert.Equal(t, map[resolution.Target]string{
		testSyncTarget(t, "linux_amd64"): lockHashPrefix + planHashC,
		testSyncTarget(t, "macos_arm64"): lockHashPrefix + planHashD,
	}, artifactTargets(pd.Entry))

	// With a constraint, the newest matching version is kept as well.
	f = hostlessPlanFixture(t, "windows_amd64", true)
	planned, _, err = f.planner.plan(listOf(map[string]driverSpec{"alpha": {Version: constraint(t, "<1.1.0")}}), parsedLockFile{})
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", planned[0].Entry.Version)
	assert.Len(t, planned[0].Entry.Artifacts, 2)
}

// TestPlanWithoutHostBuildNeedsAPublishedHash pins that no archive of another
// target is downloaded: without any published hash, the driver cannot be
// locked on this host.
func TestPlanWithoutHostBuildNeedsAPublishedHash(t *testing.T) {
	f := hostlessPlanFixture(t, "macos_amd64", true)
	_, _, err := f.planner.plan(listOf(map[string]driverSpec{"beta": {}}), parsedLockFile{})
	require.ErrorContains(t, err, "version 1.0.0 has no build for macos_amd64")
	assert.ErrorContains(t, err, "publishes no archive hash for any other platform")
}

// TestPlanSyncFailsWithoutHostBuild pins that dbc sync resolves the same
// version as dbc lock and fails, instead of choosing an older version or
// another registry.
func TestPlanSyncFailsWithoutHostBuild(t *testing.T) {
	f := hostlessPlanFixture(t, "macos_amd64", false)
	_, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), parsedLockFile{})
	require.ErrorContains(t, err, "no package found for platform 'macos_amd64'")
}

// TestPlanHostBuildProblemsAreNotAMissingBuild pins that only a listed
// version without a host package is locked without one: a host package with
// invalid metadata is still an error.
func TestPlanHostBuildProblemsAreNotAMissingBuild(t *testing.T) {
	f := hostlessPlanFixture(t, planTarget, true)
	_, _, err := f.planner.plan(listOf(map[string]driverSpec{"bad-current": {}}), parsedLockFile{})
	require.ErrorContains(t, err, "invalid archive hash")
}

// TestPlanV1ProofNeedsHostBuild pins that a v1 checksum recorded for this
// platform, which only the host archive can prove, stops the upgrade when the
// version has no host build.
func TestPlanV1ProofNeedsHostBuild(t *testing.T) {
	f := hostlessPlanFixture(t, "macos_amd64", true)
	lock := v1Lock(lockInfo{Name: "alpha", Version: semver.MustParse("1.1.0"), Platform: "macos_amd64", Checksum: strings.Repeat("f", 64)})
	_, _, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), lock)
	require.ErrorContains(t, err, "records a driver library checksum for macos_amd64")
}

// TestPlanKeepsLockEntryWithoutHostBuild pins that dbc lock keeps a lock entry
// that has no artifact for this host when the locked version has no build
// for it, while dbc sync cannot complete it.
func TestPlanKeepsLockEntryWithoutHostBuild(t *testing.T) {
	entry := lockedAlpha("1.0.0", pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA))
	f := hostlessPlanFixture(t, "windows_amd64", true)
	planned, warnings, err := f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), v2Lock(entry))
	require.NoError(t, err)
	require.Len(t, planned, 1)
	assert.Equal(t, []string{"warning: driver alpha 1.0.0 has no build for this platform (windows_amd64); dbc sync will fail here"}, warnings)
	assert.False(t, planned[0].hasHostPackage())
	assert.Equal(t, entry, planned[0].Entry)
	assert.Len(t, f.boundIndexed, 1, "the locked registry is asked whether a host build exists")

	f = hostlessPlanFixture(t, "windows_amd64", false)
	_, _, err = f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), v2Lock(entry))
	require.ErrorContains(t, err, "could not complete the lock entry of driver alpha")
}

// hostlessBase serves test-driver-1 1.0.0 with a build only for otherTarget,
// so the host of the test has none.
func hostlessBase(hash string) baseModel {
	base := testBaseModel()
	index := func() ([]dbc.Driver, error) {
		return decodeTestIndex(fmt.Sprintf(`drivers:
  - name: Test Driver 1
    path: test-driver-1
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: %s
            url: test-driver-1/1.0.0/test-driver-1-other.tar.gz
            hash: sha256:%s
`, otherTarget, hash))
	}
	base.getDriverRegistry = index
	base.getRegistryIndex = func(dbc.Registry) ([]dbc.Driver, error) { return index() }
	return base
}

func (suite *SubcommandTestSuite) TestLockWithoutHostBuild() {
	listPath := suite.writeDriverList("test-driver-1")
	var counters syncCounters
	out := suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(counters.counted(hostlessBase(strings.Repeat("a", 64)))))
	suite.Contains(out, "test-driver-1-1.0.0")
	suite.Zero(counters.downloads, "no archive of another platform is downloaded")
	d := suite.lockedDriverEntry("test-driver-1")
	suite.Equal(map[resolution.Target]string{
		testSyncTarget(suite.T(), otherTarget): lockHashPrefix + strings.Repeat("a", 64),
	}, artifactTargets(d))

	// dbc sync resolves the same version from the lock and fails to install it.
	before := suite.readLockBytes()
	out = suite.runCmdErr(SyncCmd{Path: listPath}.GetModelCustom(hostlessBase(strings.Repeat("a", 64))))
	suite.Contains(out, "test-driver-1")
	suite.Contains(out, config.PlatformTuple())
	suite.Equal(before, suite.readLockBytes())
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestLockWithoutHostBuildJSON() {
	listPath := suite.writeDriverList("test-driver-1")
	out := suite.runCmd(LockCmd{Path: listPath, Json: true}.GetModelCustom(hostlessBase(strings.Repeat("a", 64))))
	var envelope struct {
		Payload jsonschema.LockStatus `json:"payload"`
	}
	suite.Require().NoError(json.Unmarshal([]byte(out), &envelope))
	suite.Equal([]jsonschema.LockedDriver{{Name: "test-driver-1", Version: "1.0.0"}}, envelope.Payload.Drivers)
}
