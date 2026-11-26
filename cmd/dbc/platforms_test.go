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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// droppedPlatformIndex is the example of issue #592: 3.0.0 drops Intel macOS.
var droppedPlatformIndex = fmt.Sprintf(`drivers:
  - name: Gamma
    path: gamma
    pkginfo:
      - version: v3.0.0
        packages:
          - platform: linux_amd64
            hash: sha256:%[1]s
          - platform: macos_arm64
            hash: sha256:%[2]s
      - version: v2.5.0
        packages:
          - platform: linux_amd64
            hash: sha256:%[3]s
          - platform: macos_arm64
            hash: sha256:%[4]s
          - platform: macos_amd64
            hash: sha256:%[5]s
`, planHashA, planHashB, planHashC, planHashD, planHashE)

func listWithPlatforms(platforms []string, drivers map[string]driverSpec) DriversList {
	return DriversList{Platforms: platforms, Drivers: drivers}
}

func TestTargetPlatforms(t *testing.T) {
	targets, err := DriversList{}.targetPlatforms()
	require.NoError(t, err)
	assert.Nil(t, targets)

	targets, err = DriversList{Platforms: []string{"macos_arm64", "linux_amd64"}}.targetPlatforms()
	require.NoError(t, err)
	assert.Equal(t, []resolution.Target{testSyncTarget(t, "linux_amd64"), testSyncTarget(t, "macos_arm64")}, targets, "sorted")

	for _, tc := range []struct {
		platforms []string
		want      string
	}{
		{[]string{}, "must not be empty"},
		{[]string{"linux"}, "invalid platform tuple"},
		{[]string{"macos_arm64", "darwin_arm64"}, `"darwin_arm64" is listed more than once`},
	} {
		_, err := DriversList{Platforms: tc.platforms}.targetPlatforms()
		assert.ErrorContains(t, err, tc.want, tc.platforms)
	}
}

// TestPlanSelectsVersionForEveryDeclaredPlatform follows issue #592: with
// Intel macOS declared, every host locks 2.5.0 and records all declared
// platforms; without the declaration, platforms do not matter.
func TestPlanSelectsVersionForEveryDeclaredPlatform(t *testing.T) {
	list := listWithPlatforms([]string{"linux_amd64", "macos_arm64", "macos_amd64"}, map[string]driverSpec{"gamma": {}})
	for _, host := range []string{"linux_amd64", "macos_amd64"} {
		planner := targetTestPlanner(host, droppedPlatformIndex)
		planned, _, err := planner.plan(list, parsedLockFile{})
		require.NoError(t, err, host)
		assert.Equal(t, "2.5.0", planned[0].Entry.Version, host)
		assert.Equal(t, map[resolution.Target]string{
			testSyncTarget(t, "linux_amd64"): lockHashPrefix + planHashC,
			testSyncTarget(t, "macos_arm64"): lockHashPrefix + planHashD,
			testSyncTarget(t, "macos_amd64"): lockHashPrefix + planHashE,
		}, artifactTargets(planned[0].Entry), host)
		assert.True(t, planned[0].hasHostPackage(), host)
	}

	planner := targetTestPlanner("macos_amd64", droppedPlatformIndex)
	planner.hostOptional = true
	planned, _, err := planner.plan(listOf(map[string]driverSpec{"gamma": {}}), parsedLockFile{})
	require.NoError(t, err)
	assert.Equal(t, "3.0.0", planned[0].Entry.Version)
}

// TestPlanRecordsPublishedPlatformsToo pins that, as without a declaration,
// a platform that the driver list does not declare is recorded when the
// registry publishes its hash.
func TestPlanRecordsPublishedPlatformsToo(t *testing.T) {
	planner := targetTestPlanner("linux_amd64", droppedPlatformIndex)
	planned, _, err := planner.plan(listWithPlatforms([]string{"linux_amd64"}, map[string]driverSpec{"gamma": {}}), parsedLockFile{})
	require.NoError(t, err)
	assert.Equal(t, "3.0.0", planned[0].Entry.Version)
	assert.Equal(t, map[resolution.Target]string{
		testSyncTarget(t, "linux_amd64"): lockHashPrefix + planHashA,
		testSyncTarget(t, "macos_arm64"): lockHashPrefix + planHashB,
	}, artifactTargets(planned[0].Entry))
}

// TestPlanWithoutCommonVersionIsNotAMiss pins that a version without a build
// for a declared platform never makes another registry serve the driver.
func TestPlanWithoutCommonVersionIsNotAMiss(t *testing.T) {
	planner := targetTestPlanner("linux_amd64", droppedPlatformIndex)
	list := listWithPlatforms([]string{"linux_amd64", "windows_amd64"}, map[string]driverSpec{"gamma": {}})
	_, _, err := planner.plan(list, parsedLockFile{})
	require.ErrorContains(t, err, "no version of driver gamma that the driver list allows has a build for every platform in platforms (linux_amd64, windows_amd64)")
	assert.NotErrorIs(t, err, dbc.ErrPackageMiss)
}

// TestPlanMeasuresDeclaredPlatformWithoutHash pins that a declared platform
// is treated like the current one: without a published hash, its archive is
// planned for download to measure it.
func TestPlanMeasuresDeclaredPlatformWithoutHash(t *testing.T) {
	f := newPlanFixture(t)
	list := listWithPlatforms([]string{"linux_amd64", "windows_amd64"}, map[string]driverSpec{"alpha": {}})
	planned, _, err := f.planner.plan(list, parsedLockFile{})
	require.NoError(t, err)
	pd := planned[0]
	assert.Equal(t, "1.1.0", pd.Entry.Version)
	assert.Equal(t, map[resolution.Target]string{
		testSyncTarget(t, "linux_amd64"):   lockHashPrefix + planHashC,
		testSyncTarget(t, "macos_arm64"):   lockHashPrefix + planHashD,
		testSyncTarget(t, "windows_amd64"): "",
	}, artifactTargets(pd.Entry))
	require.Len(t, pd.Unmeasured, 1)
	assert.Equal(t, "windows_amd64", pd.Unmeasured[0].PlatformTuple)
}

// TestPlanUndeclaredHost pins that dbc sync stops on a platform the driver list
// does not declare, while dbc lock records the declared platforms only.
func TestPlanUndeclaredHost(t *testing.T) {
	list := listWithPlatforms([]string{"linux_amd64", "macos_arm64"}, map[string]driverSpec{"gamma": {}})
	planner := targetTestPlanner("macos_amd64", droppedPlatformIndex)
	_, _, err := planner.plan(list, parsedLockFile{})
	require.ErrorContains(t, err, "this platform (macos_amd64) is not in platforms of the driver list (linux_amd64, macos_arm64)")

	planner.hostOptional = true
	planned, warnings, err := planner.plan(list, parsedLockFile{})
	require.NoError(t, err)
	assert.Equal(t, []string{"warning: this platform (macos_amd64) is not in platforms of the driver list (linux_amd64, macos_arm64), so dbc sync cannot install its drivers here"}, warnings)
	assert.False(t, planned[0].hasHostPackage())
	assert.Equal(t, "3.0.0", planned[0].Entry.Version)
	assert.Len(t, planned[0].Entry.Artifacts, 2)

	// A lock entry for the same declaration is replayed without adding the
	// undeclared platform or fetching any index.
	lock := v2Lock(planned[0].Entry)
	lock.V2.Platforms = planner.platforms
	planner = targetTestPlanner("macos_amd64", droppedPlatformIndex)
	planner.hostOptional = true
	planner.index = func() ([]dbc.Driver, error) { t.Fatal("no index may be fetched"); return nil, nil }
	replayed, _, err := planner.plan(list, lock)
	require.NoError(t, err)
	assert.Equal(t, planned[0].Entry, replayed[0].Entry)
}

// TestPlanChangedPlatformsUndeclaredHost pins that dbc lock on an undeclared
// platform completes the declared platforms, keeps the locked artifact of
// this platform and warns once about the declaration.
func TestPlanChangedPlatformsUndeclaredHost(t *testing.T) {
	host := pathArtifact(t, "windows_amd64", "alpha/1.1.0/alpha-windows.tar.gz", strings.Repeat("f", 64))
	entry := lockedAlpha("1.1.0", pathArtifact(t, "linux_amd64", "alpha/1.1.0/alpha-linux.tar.gz", planHashC), host)
	f := hostlessPlanFixture(t, "windows_amd64", true)
	planned, warnings, err := f.planner.plan(listWithPlatforms([]string{"linux_amd64", "macos_arm64"}, map[string]driverSpec{"alpha": {}}), v2Lock(entry))
	require.NoError(t, err)
	assert.Equal(t, []string{"warning: this platform (windows_amd64) is not in platforms of the driver list (linux_amd64, macos_arm64), so dbc sync cannot install its drivers here"}, warnings)
	assert.Equal(t, "1.1.0", planned[0].Entry.Version)
	assert.False(t, planned[0].hasHostPackage(), "nothing is downloaded for this platform")
	assert.Contains(t, planned[0].Entry.Artifacts, host)
	assert.Contains(t, artifactTargets(planned[0].Entry), testSyncTarget(t, "macos_arm64"))
}

// TestPlanChangedPlatformsResolveAgain pins that a lock written for another
// declaration keeps the locked source, version and hashes where the version
// has a build for every declared platform, and is resolved again otherwise.
func TestPlanChangedPlatformsResolveAgain(t *testing.T) {
	// A hash that the registry no longer publishes is kept as locked.
	locked := strings.Repeat("f", 64)
	entry := lockedAlpha("1.0.0", pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", locked))
	list := listWithPlatforms([]string{"linux_amd64", "macos_arm64"}, map[string]driverSpec{"alpha": {}})
	f := newPlanFixture(t)
	planned, _, err := f.planner.plan(list, v2Lock(entry))
	require.NoError(t, err)
	assert.Zero(t, f.indexCalls, "only the locked registry is asked")
	assert.Len(t, f.boundIndexed, 1)
	assert.Equal(t, "1.0.0", planned[0].Entry.Version, "the locked version has both builds")
	assert.Equal(t, map[resolution.Target]string{
		testSyncTarget(t, "linux_amd64"): lockHashPrefix + locked,
		testSyncTarget(t, "macos_arm64"): lockHashPrefix + planHashB,
	}, artifactTargets(planned[0].Entry))
	assert.Equal(t, locked, planned[0].Package.ArchiveSHA256, "the host archive is checked against the locked hash")

	// The same declaration replays the entry.
	lock := v2Lock(planned[0].Entry)
	lock.V2.Platforms = f.planner.platforms
	f = newPlanFixture(t)
	replayed, _, err := f.planner.plan(list, lock)
	require.NoError(t, err)
	assert.Zero(t, f.indexCalls)
	assert.Empty(t, f.boundIndexed)
	assert.Equal(t, planned[0].Entry, replayed[0].Entry)

	// Removing the declaration replays the entry as well.
	f = newPlanFixture(t)
	replayed, _, err = f.planner.plan(listOf(map[string]driverSpec{"alpha": {}}), lock)
	require.NoError(t, err)
	assert.Zero(t, f.indexCalls)
	assert.Equal(t, planned[0].Entry, replayed[0].Entry)

	// Without a build of the locked version for every declared platform, the
	// driver is resolved again.
	f = newPlanFixture(t)
	planned, warnings, err := f.planner.plan(listWithPlatforms([]string{"linux_amd64", "windows_amd64"}, map[string]driverSpec{"alpha": {}}), v2Lock(entry))
	require.NoError(t, err)
	assert.Equal(t, 1, f.indexCalls)
	assert.Equal(t, "1.1.0", planned[0].Entry.Version)
	assert.Equal(t, []string{"warning: driver alpha: version 1.0.0 from https://registry.example.com in the lock file has no build for windows_amd64 in platforms, " +
		"so version 1.1.0 from https://registry.example.com was selected instead"}, warnings)

	// A changed constraint resolves it again without that warning.
	f = newPlanFixture(t)
	_, warnings, err = f.planner.plan(listWithPlatforms([]string{"linux_amd64", "windows_amd64"}, map[string]driverSpec{"alpha": {Version: constraint(t, ">=1.1.0")}}), v2Lock(entry))
	require.NoError(t, err)
	assert.Empty(t, warnings)
}

// TestPlanChangedPlatformsKeepsLockEvidence pins that relock keeps every
// artifact a lock entry records, declared or not, even when the registry no
// longer lists it or publishes another hash, and only checks the declared
// platforms that the entry does not record yet.
func TestPlanChangedPlatformsKeepsLockEvidence(t *testing.T) {
	measured := strings.Repeat("f", 64)
	// 1.0.0 lists no windows_amd64 build any more; the entry recorded one.
	gone := pathArtifact(t, "windows_amd64", "alpha/1.0.0/alpha-windows.tar.gz", measured)
	// An undeclared artifact that the index does not list either.
	size := int64(42)
	extra := pathArtifact(t, "linux_arm64", "alpha/1.0.0/alpha-linux-arm64.tar.gz", measured)
	extra.Size = &size
	entry := lockedAlpha("1.0.0", pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA), gone, extra)
	f := newPlanFixture(t)
	planned, warnings, err := f.planner.plan(listWithPlatforms([]string{"linux_amd64", "macos_arm64", "windows_amd64"}, map[string]driverSpec{"alpha": {}}), v2Lock(entry))
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Zero(t, f.indexCalls, "not resolved again")
	assert.Equal(t, "1.0.0", planned[0].Entry.Version)
	assert.Equal(t, map[resolution.Target]string{
		testSyncTarget(t, "linux_amd64"):   lockHashPrefix + planHashA,
		testSyncTarget(t, "linux_arm64"):   lockHashPrefix + measured,
		testSyncTarget(t, "macos_arm64"):   lockHashPrefix + planHashB,
		testSyncTarget(t, "windows_amd64"): lockHashPrefix + measured,
	}, artifactTargets(planned[0].Entry))
	assert.Contains(t, planned[0].Entry.Artifacts, extra)

	// An undeclared artifact is kept too, with its locked hash rather than
	// the one the registry publishes now.
	entry = lockedAlpha("1.1.0",
		pathArtifact(t, "linux_amd64", "alpha/1.1.0/alpha-linux.tar.gz", planHashC),
		pathArtifact(t, "macos_arm64", "alpha/1.1.0/alpha-macos.tar.gz", measured))
	f = newPlanFixture(t)
	planned, _, err = f.planner.plan(listWithPlatforms([]string{"linux_amd64", "windows_amd64"}, map[string]driverSpec{"alpha": {}}), v2Lock(entry))
	require.NoError(t, err)
	assert.Equal(t, map[resolution.Target]string{
		testSyncTarget(t, "linux_amd64"):   lockHashPrefix + planHashC,
		testSyncTarget(t, "macos_arm64"):   lockHashPrefix + measured,
		testSyncTarget(t, "windows_amd64"): "",
	}, artifactTargets(planned[0].Entry))
	require.Len(t, planned[0].Unmeasured, 1)
	assert.Equal(t, "windows_amd64", planned[0].Unmeasured[0].PlatformTuple)
}

// TestPlanChangedPlatformsDoesNotResolveElsewhere pins that relock, like
// fromLock, stops when the locked registry cannot complete the entry, instead
// of resolving the driver again from the merged index.
func TestPlanChangedPlatformsDoesNotResolveElsewhere(t *testing.T) {
	entry := lockedAlpha("1.0.0", pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA))
	list := listWithPlatforms([]string{"linux_amd64", "macos_arm64"}, map[string]driverSpec{"alpha": {}})

	t.Run("registry with query", func(t *testing.T) {
		f := newPlanFixture(t)
		f.registries = []dbc.Registry{{BaseURL: must(url.Parse("https://registry.example.com?tenant=SUPERSECRET"))}}
		_, _, err := f.planner.plan(list, v2Lock(entry))
		require.ErrorContains(t, err, "the lock file has no artifact of driver alpha for macos_arm64 and its registry https://registry.example.com uses a query string")
		assert.NotContains(t, err.Error(), "SUPERSECRET")
		assert.Empty(t, f.boundIndexed)
		assert.Zero(t, f.indexCalls)
	})
	t.Run("driver gone from the locked registry", func(t *testing.T) {
		f := newPlanFixture(t)
		f.planner.registryIndex = func(dbc.Registry) ([]dbc.Driver, error) { return nil, nil }
		_, _, err := f.planner.plan(list, v2Lock(entry))
		require.ErrorContains(t, err, "driver `alpha` not found in driver registry index")
		assert.Zero(t, f.indexCalls)
	})
}

// TestPlanDropsV1ProofOfDroppedPin pins that a v1 checksum is not checked
// against another version than the one it was recorded for.
func TestPlanDropsV1ProofOfDroppedPin(t *testing.T) {
	f := newPlanFixture(t)
	lock := v1Lock(lockInfo{Name: "alpha", Version: semver.MustParse("1.0.0"), Platform: planTarget, Checksum: strings.Repeat("f", 64)})
	planned, warnings, err := f.planner.plan(listWithPlatforms([]string{"linux_amd64", "windows_amd64"}, map[string]driverSpec{"alpha": {}}), lock)
	require.NoError(t, err)
	assert.Equal(t, "1.1.0", planned[0].Entry.Version)
	assert.Empty(t, planned[0].LibrarySHA256)
	assert.Equal(t, []string{"warning: driver alpha: version 1.0.0 in the lock file does not cover all declared platforms, so version 1.1.0 was selected instead, " +
		"ignoring the checksum in the lock file"}, warnings)

	// A pin without a checksum is reported as well.
	f = newPlanFixture(t)
	lock = v1Lock(lockInfo{Name: "alpha", Version: semver.MustParse("1.0.0")})
	_, warnings, err = f.planner.plan(listWithPlatforms([]string{"linux_amd64", "windows_amd64"}, map[string]driverSpec{"alpha": {}}), lock)
	require.NoError(t, err)
	assert.Equal(t, []string{"warning: driver alpha: version 1.0.0 in the lock file does not cover all declared platforms, so version 1.1.0 was selected instead"}, warnings)
}

// TestPlanAmbiguousPlatformIsAnError pins that several packages for a declared
// platform make the selected version an error rather than a reason to select
// an older version.
func TestPlanAmbiguousPlatformIsAnError(t *testing.T) {
	index := fmt.Sprintf(`drivers:
  - name: Delta
    path: delta
    pkginfo:
      - version: v2.0.0
        packages:
          - platform: macos_arm64
            hash: sha256:%[1]s
          - platform: darwin_arm64
            hash: sha256:%[2]s
      - version: v1.0.0
        packages:
          - platform: macos_arm64
            hash: sha256:%[3]s
`, planHashA, planHashB, planHashC)
	planner := targetTestPlanner("macos_arm64", index)
	_, _, err := planner.plan(listWithPlatforms([]string{"macos_arm64"}, map[string]driverSpec{"delta": {}}), parsedLockFile{})
	require.ErrorContains(t, err, "version 2.0.0 of driver delta lists more than one package for macos_arm64, so its build for that platform is ambiguous")
}

func TestLockFilePlatformsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbc.lock")
	entry := lockedAlpha("1.0.0",
		pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA),
		pathArtifact(t, "macos_arm64", "alpha/1.0.0/alpha-macos.tar.gz", planHashB))
	lf := lockFileV2{Version: lockFileVersionV2, Platforms: []resolution.Target{testSyncTarget(t, "macos_arm64"), testSyncTarget(t, "linux_amd64")}, Drivers: []lockedDriver{entry}}
	require.NoError(t, writeLockFileV2(path, lf))
	read, err := readLockFile(path)
	require.NoError(t, err)
	assert.Equal(t, []resolution.Target{testSyncTarget(t, "linux_amd64"), testSyncTarget(t, "macos_arm64")}, read.V2.Platforms)

	lf.Platforms = []resolution.Target{testSyncTarget(t, "windows_amd64")}
	assert.ErrorContains(t, writeLockFileV2(path, lf), "no artifact for the declared platform")
	lf.Platforms = []resolution.Target{testSyncTarget(t, "linux_amd64"), testSyncTarget(t, "linux_amd64")}
	assert.ErrorContains(t, writeLockFileV2(path, lf), "duplicate platform")
	lf.Platforms = []resolution.Target{{OS: "darwin", Arch: "arm64"}}
	assert.ErrorContains(t, writeLockFileV2(path, lf), "non-canonical platform")
}

func (suite *SubcommandTestSuite) TestAddChecksDeclaredPlatforms() {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	base := testBaseModel()
	base.getDriverRegistry = func() ([]dbc.Driver, error) { return decodeTestIndex(droppedPlatformIndex) }

	// No build for this platform is needed, and the declaration is kept.
	suite.Require().NoError(os.WriteFile(listPath, []byte("platforms = ['linux_amd64', 'macos_arm64', 'macos_amd64']\n[drivers]\n"), 0o644))
	suite.runCmd(AddCmd{Path: listPath, Driver: []string{"gamma"}}.GetModelCustom(base))
	data, err := os.ReadFile(listPath)
	suite.Require().NoError(err)
	suite.Contains(string(data), "platforms = ['linux_amd64', 'macos_arm64', 'macos_amd64']")
	suite.Contains(string(data), "[drivers.gamma]")

	suite.Require().NoError(os.WriteFile(listPath, []byte("platforms = ['windows_amd64']\n[drivers]\n"), 0o644))
	out := suite.runCmdErr(AddCmd{Path: listPath, Driver: []string{"gamma"}}.GetModelCustom(base))
	suite.Contains(out, "has a build for every platform in platforms (windows_amd64)")
}

func (suite *SubcommandTestSuite) TestLockRecordsDeclaredPlatforms() {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(listPath, fmt.Appendf(nil, "platforms = ['%s']\n[drivers.test-driver-1]\n", otherTarget), 0o644))
	var counters syncCounters
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(counters.counted(multiTargetBase(strings.Repeat("b", 64)))))
	suite.Zero(counters.downloads)
	lf := suite.readV2Lock()
	suite.Equal([]resolution.Target{testSyncTarget(suite.T(), otherTarget)}, lf.Platforms)
	// The published hash of this undeclared platform is recorded too.
	suite.Equal(map[resolution.Target]string{
		testSyncTarget(suite.T(), otherTarget):            lockHashPrefix + strings.Repeat("a", 64),
		testSyncTarget(suite.T(), config.PlatformTuple()): lockHashPrefix + strings.Repeat("b", 64),
	}, artifactTargets(suite.lockedDriverEntry("test-driver-1")))

	// dbc sync on this undeclared platform stops before installing anything.
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(multiTargetBase(strings.Repeat("b", 64))))
	suite.Contains(out, "is not in platforms of the driver list")
	suite.driverIsNotInstalled("test-driver-1")
}

// declaredOtherBase serves test-driver-1 1.0.0 for the current platform with a
// published hash and for otherTarget without one; download returns the
// archive of otherTarget.
func declaredOtherBase(suite *SubcommandTestSuite, other string) baseModel {
	base := testBaseModel()
	index := func() ([]dbc.Driver, error) {
		return decodeTestIndex(fmt.Sprintf(`drivers:
  - name: Test Driver 1
    path: test-driver-1
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: %s
            url: test-driver-1/1.0.0/test-driver-1.tar.gz
            hash: sha256:%s
          - platform: %s
            url: test-driver-1/1.0.0/test-driver-1-other.tar.gz
`, config.PlatformTuple(), testArchiveSHA256(suite, "test-driver-1.tar.gz"), otherTarget))
	}
	base.getDriverRegistry = index
	base.getRegistryIndex = func(dbc.Registry) ([]dbc.Driver, error) { return index() }
	base.downloadPkg = func(pkg dbc.PkgInfo) (*os.File, error) {
		if pkg.PlatformTuple == otherTarget {
			return openTestdataCopy(other)
		}
		return openTestdataCopy("test-driver-1.tar.gz")
	}
	return base
}

func (suite *SubcommandTestSuite) writeDeclaredList() string {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(listPath, fmt.Appendf(nil, "platforms = ['%s', '%s']\n[drivers.test-driver-1]\n", config.PlatformTuple(), otherTarget), 0o644))
	return listPath
}

// TestLockMeasuresDeclaredPlatform pins that dbc lock downloads the archive of
// a declared platform without a published hash and records its hash, like the
// archive of the current platform.
func (suite *SubcommandTestSuite) TestLockMeasuresDeclaredPlatform() {
	listPath := suite.writeDeclaredList()
	var counters syncCounters
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(counters.counted(declaredOtherBase(suite, "test-driver-1.tar.gz"))))
	suite.Equal(1, counters.downloads, "only the platform without a published hash")
	suite.Require().Len(counters.downloadDirs, 1)
	suite.NoDirExists(counters.downloadDirs[0], "the download is removed")
	suite.Equal(lockHashPrefix+testArchiveSHA256(suite, "test-driver-1.tar.gz"), artifactTargets(suite.lockedDriverEntry("test-driver-1"))[testSyncTarget(suite.T(), otherTarget)])

	// The archive is neither extracted nor verified: that is left to the
	// installation on its platform.
	for _, archive := range []string{"test-driver-tampered.tar.gz", "test-driver-invalid-manifest.tar.gz"} {
		suite.Require().NoError(os.Remove(suite.lockPath()))
		suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(declaredOtherBase(suite, archive)))
		suite.Equal(lockHashPrefix+testArchiveSHA256(suite, archive), artifactTargets(suite.lockedDriverEntry("test-driver-1"))[testSyncTarget(suite.T(), otherTarget)], archive)
	}
}

// TestSyncMeasuresDeclaredPlatform pins that dbc sync, which writes the lock
// file too, measures a declared platform without a published hash, also when
// the driver of the current platform is already installed.
func (suite *SubcommandTestSuite) TestSyncMeasuresDeclaredPlatform() {
	listPath := suite.writeDeclaredList()
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(declaredOtherBase(suite, "test-driver-1.tar.gz")))
	suite.driverIsInstalled("test-driver-1", true)
	suite.Require().NoError(os.Remove(suite.lockPath()))

	var counters syncCounters
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(counters.counted(declaredOtherBase(suite, "test-driver-1.tar.gz"))))
	suite.Equal(1, counters.downloads, "the installed driver is reused; only the other platform is downloaded")
	suite.Equal(lockHashPrefix+testArchiveSHA256(suite, "test-driver-1.tar.gz"), artifactTargets(suite.lockedDriverEntry("test-driver-1"))[testSyncTarget(suite.T(), otherTarget)])
}

// TestPlanChangedPlatformsCoveredByLock pins that an entry that already
// records every declared platform is kept without fetching any index, so
// neither its registry nor its hashes depend on what the registries list now.
func TestPlanChangedPlatformsCoveredByLock(t *testing.T) {
	entry := lockedAlpha("1.0.0",
		pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA),
		pathArtifact(t, "macos_arm64", "alpha/1.0.0/alpha-macos.tar.gz", planHashB))
	f := newPlanFixture(t)
	f.planner.registryIndex = func(dbc.Registry) ([]dbc.Driver, error) { t.Fatal("no index may be fetched"); return nil, nil }
	planned, _, err := f.planner.plan(listWithPlatforms([]string{"linux_amd64", "macos_arm64"}, map[string]driverSpec{"alpha": {}}), v2Lock(entry))
	require.NoError(t, err)
	assert.Zero(t, f.indexCalls)
	assert.Equal(t, entry, planned[0].Entry)
}

// TestPlanChangedPlatformsKeepsLockedSize pins that the locked archive size of
// the current platform is still checked when an entry is extended.
func TestPlanChangedPlatformsKeepsLockedSize(t *testing.T) {
	size := int64(1234)
	host := pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA)
	host.Size = &size
	f := newPlanFixture(t)
	planned, _, err := f.planner.plan(listWithPlatforms([]string{"linux_amd64", "macos_arm64"}, map[string]driverSpec{"alpha": {}}), v2Lock(lockedAlpha("1.0.0", host)))
	require.NoError(t, err)
	require.Len(t, f.boundIndexed, 1, "the missing platform is added from the locked registry")
	require.NotNil(t, planned[0].ArchiveSize)
	assert.Equal(t, size, *planned[0].ArchiveSize)
	assert.Equal(t, planHashA, planned[0].Package.ArchiveSHA256)
}
