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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lockedNamed(name, version string, artifacts ...lockedArtifact) lockedDriver {
	d := lockedAlpha(version, artifacts...)
	d.Name = name
	return d
}

func TestLockMismatches(t *testing.T) {
	linux := pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA)
	macos := pathArtifact(t, "macos_arm64", "alpha/1.0.0/alpha-macos.tar.gz", planHashB)
	beta := lockedNamed("beta", "2.0.0-beta.1", linux)
	registries := []dbc.RegistryEntry{{Name: "example", URL: "https://registry.example.com"}, {Name: "other", URL: "https://other.example.com"}}
	named := func(name string) *driverSource { return &driverSource{Type: sourceTypeRegistry, Name: &name} }

	tests := []struct {
		name  string
		list  DriversList
		lock  lockFileV2
		wants []string
	}{
		{
			name: "matching",
			list: listOf(map[string]driverSpec{"alpha": {Version: constraint(t, ">=1.0.0")}}),
			lock: v2Lock(lockedAlpha("1.0.0", linux)).V2,
		},
		{
			name: "missing and extra drivers",
			list: listOf(map[string]driverSpec{"alpha": {}, "gamma": {}}),
			lock: v2Lock(lockedAlpha("1.0.0", linux), lockedNamed("delta", "1.0.0", linux)).V2,
			wants: []string{
				"driver delta is in the lock file but not in the driver list",
				"driver gamma is in the driver list but not in the lock file",
			},
		},
		{
			name:  "version constraint",
			list:  listOf(map[string]driverSpec{"alpha": {Version: constraint(t, ">=1.1.0")}}),
			lock:  v2Lock(lockedAlpha("1.0.0", linux)).V2,
			wants: []string{"driver alpha: version 1.0.0 in the lock file does not satisfy >=1.1.0"},
		},
		{
			name:  "pre-release not allowed",
			list:  listOf(map[string]driverSpec{"beta": {}}),
			lock:  v2Lock(beta).V2,
			wants: []string{"driver beta: version 2.0.0-beta.1 in the lock file is a pre-release, which the driver list does not allow"},
		},
		{
			// Without prerelease = "allow" applied, the constraint would not
			// match the pre-release, unlike in dbc sync.
			name: "constraint with pre-releases allowed",
			list: listOf(map[string]driverSpec{"beta": {Version: constraint(t, ">=2.0.0-0"), Prerelease: "allow"}}),
			lock: v2Lock(beta).V2,
		},
		{
			name: "same source",
			list: DriversList{Registries: registries, Drivers: map[string]driverSpec{"alpha": {Source: named("example")}}},
			lock: v2Lock(lockedAlpha("1.0.0", linux)).V2,
		},
		{
			name: "other source and invalid source are both reported",
			list: DriversList{Registries: registries, Drivers: map[string]driverSpec{
				"alpha": {Source: named("other")},
				"beta":  {Source: named("missing"), Prerelease: "allow"},
			}},
			lock: v2Lock(lockedAlpha("1.0.0", linux), beta).V2,
			wants: []string{
				"driver alpha: the driver list takes it from https://other.example.com, but the lock file has it from https://registry.example.com",
				"driver beta: ",
			},
		},
		{
			name: "platforms in another order",
			list: DriversList{Platforms: []string{"macos_arm64", "linux_amd64"}, Drivers: map[string]driverSpec{"alpha": {}}},
			lock: lockFileV2{
				Platforms: []resolution.Target{testSyncTarget(t, "macos_arm64"), testSyncTarget(t, "linux_amd64")},
				Drivers:   []lockedDriver{lockedAlpha("1.0.0", macos, linux)},
			},
		},
		{
			name: "platform alias",
			list: DriversList{Platforms: []string{"linux_amd64_gnu"}, Drivers: map[string]driverSpec{"alpha": {}}},
			lock: lockFileV2{Platforms: []resolution.Target{testSyncTarget(t, "linux_amd64")}, Drivers: []lockedDriver{lockedAlpha("1.0.0", linux)}},
		},
		{
			name:  "platforms added",
			list:  DriversList{Platforms: []string{"linux_amd64", "macos_arm64"}, Drivers: map[string]driverSpec{"alpha": {}}},
			lock:  lockFileV2{Platforms: []resolution.Target{testSyncTarget(t, "linux_amd64")}, Drivers: []lockedDriver{lockedAlpha("1.0.0", linux)}},
			wants: []string{"the driver list declares platforms linux_amd64, macos_arm64, but the lock file was created for platforms linux_amd64"},
		},
		{
			name:  "platforms removed",
			list:  listOf(map[string]driverSpec{"alpha": {}}),
			lock:  lockFileV2{Platforms: []resolution.Target{testSyncTarget(t, "linux_amd64")}, Drivers: []lockedDriver{lockedAlpha("1.0.0", linux)}},
			wants: []string{"the driver list declares no platforms, but the lock file was created for platforms linux_amd64"},
		},
		{
			name:  "platforms declared",
			list:  DriversList{Platforms: []string{"linux_amd64"}, Drivers: map[string]driverSpec{"alpha": {}}},
			lock:  v2Lock(lockedAlpha("1.0.0", linux)).V2,
			wants: []string{"the driver list declares platforms linux_amd64, but the lock file was created for no platforms"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lockMismatches(tt.list, tt.lock)
			require.NoError(t, err)
			require.Len(t, got, len(tt.wants), "%q", got)
			for i, want := range tt.wants {
				assert.Contains(t, got[i], want)
			}
		})
	}
}

func TestPlanLocked(t *testing.T) {
	linux := pathArtifact(t, "linux_amd64", "alpha/1.0.0/alpha-linux.tar.gz", planHashA)
	macos := pathArtifact(t, "macos_arm64", "alpha/1.0.0/alpha-macos.tar.gz", planHashB)

	t.Run("replays every entry without an index", func(t *testing.T) {
		f := newPlanFixture(t)
		lock := v2Lock(lockedAlpha("1.0.0", linux, macos), lockedNamed("beta", "1.0.0", linux))
		planned, err := planLocked(&f.planner, listOf(map[string]driverSpec{"alpha": {}, "beta": {}}), lock.V2)
		require.NoError(t, err)
		require.Len(t, planned, 2)
		assert.Equal(t, "alpha", planned[0].Name)
		assert.Equal(t, lock.V2.Drivers[0], planned[0].Entry, "the entry is kept as it is")
		assert.Equal(t, planHashA, planned[0].Package.ArchiveSHA256)
		assert.Equal(t, "https://registry.example.com/alpha/1.0.0/alpha-linux.tar.gz", planned[0].Package.Path.String())
		assert.Zero(t, f.indexCalls)
		assert.Empty(t, f.boundIndexed)
	})

	t.Run("a missing host artifact is not completed", func(t *testing.T) {
		f := newPlanFixture(t)
		// alpha comes first, so the plan must not stop before checking beta.
		lock := v2Lock(lockedAlpha("1.0.0", linux), lockedNamed("beta", "1.0.0", macos))
		_, err := planLocked(&f.planner, listOf(map[string]driverSpec{"alpha": {}, "beta": {}}), lock.V2)
		var lockedErr *lockedSyncError
		require.ErrorAs(t, err, &lockedErr)
		assert.Equal(t, []string{"driver beta 1.0.0 has no artifact for this platform (linux_amd64) in the lock file"}, lockedErr.mismatches)
		assert.Empty(t, lockedErr.trust)
		assert.Contains(t, err.Error(), "run `dbc lock`")
		assert.Zero(t, f.indexCalls)
		assert.Empty(t, f.boundIndexed)
	})

	t.Run("trust problems are reported separately", func(t *testing.T) {
		f := newPlanFixture(t)
		untrusted := lockedNamed("beta", "1.0.0", linux)
		untrusted.Signer.Fingerprint = "0000000000000000000000000000000000000000"
		unbound := lockedNamed("gamma", "1.0.0", linux)
		unbound.Source.URL = "https://unknown.example.com"
		lock := v2Lock(lockedAlpha("1.0.0", linux), untrusted, unbound)
		_, err := planLocked(&f.planner, listOf(map[string]driverSpec{"alpha": {}, "beta": {}, "gamma": {}}), lock.V2)
		var lockedErr *lockedSyncError
		require.ErrorAs(t, err, &lockedErr)
		assert.Empty(t, lockedErr.mismatches)
		require.Len(t, lockedErr.trust, 2)
		assert.Contains(t, lockedErr.trust[0], "driver beta")
		assert.Contains(t, lockedErr.trust[1], "driver gamma")
		assert.Contains(t, err.Error(), "check the registries and signing keys")
		assert.NotContains(t, err.Error(), "run `dbc lock` to update the lock file")
	})

	t.Run("mismatches and problems of the plan are reported together", func(t *testing.T) {
		f := newPlanFixture(t)
		untrusted := lockedNamed("beta", "1.0.0", linux)
		untrusted.Signer.Fingerprint = "0000000000000000000000000000000000000000"
		noHost := lockedNamed("gamma", "1.0.0", macos)
		noHost.Source.URL = "https://unknown.example.com"
		extra := lockedNamed("delta", "1.0.0", macos)
		lock := v2Lock(lockedAlpha("1.0.0", linux), untrusted, noHost, extra)
		list := listOf(map[string]driverSpec{"alpha": {Version: constraint(t, ">=2")}, "beta": {}, "gamma": {}})
		_, err := planLocked(&f.planner, list, lock.V2)
		var lockedErr *lockedSyncError
		require.ErrorAs(t, err, &lockedErr)
		assert.Equal(t, []string{
			"driver alpha: version 1.0.0 in the lock file does not satisfy >=2",
			"driver delta is in the lock file but not in the driver list",
			"driver gamma 1.0.0 has no artifact for this platform (linux_amd64) in the lock file",
		}, lockedErr.mismatches, "delta, which the list lacks, is not planned")
		require.Len(t, lockedErr.trust, 2, "gamma's registry is checked although it lacks the host artifact")
		assert.Contains(t, lockedErr.trust[0], "driver beta")
		assert.Contains(t, lockedErr.trust[1], "driver gamma")
	})

	t.Run("an untrusted signer does not hide an invalid artifact location", func(t *testing.T) {
		f := newPlanFixture(t)
		d := lockedAlpha("1.0.0", lockedArtifact{
			Target:   testSyncTarget(t, planTarget),
			Format:   "tar.gz",
			Location: resolution.ArtifactLocation{Kind: "bogus", Value: "alpha.tar.gz"},
			Hash:     lockHashPrefix + planHashA,
		})
		d.Signer.Fingerprint = "0000000000000000000000000000000000000000"
		_, err := planLocked(&f.planner, listOf(map[string]driverSpec{"alpha": {}}), v2Lock(d).V2)
		var lockedErr *lockedSyncError
		require.ErrorAs(t, err, &lockedErr)
		require.Len(t, lockedErr.trust, 1)
		assert.Contains(t, lockedErr.trust[0], "driver alpha")
		require.Len(t, lockedErr.mismatches, 1)
		assert.Contains(t, lockedErr.mismatches[0], "unsupported location kind")
	})

	t.Run("an unconfigured registry leaves the artifact location unchecked", func(t *testing.T) {
		f := newPlanFixture(t)
		d := lockedAlpha("1.0.0", lockedArtifact{
			Target:   testSyncTarget(t, planTarget),
			Format:   "tar.gz",
			Location: resolution.ArtifactLocation{Kind: "bogus", Value: "alpha.tar.gz"},
			Hash:     lockHashPrefix + planHashA,
		})
		d.Source.URL = "https://unknown.example.com"
		_, err := planLocked(&f.planner, listOf(map[string]driverSpec{"alpha": {}}), v2Lock(d).V2)
		var lockedErr *lockedSyncError
		require.ErrorAs(t, err, &lockedErr)
		assert.Len(t, lockedErr.trust, 1)
		assert.Empty(t, lockedErr.mismatches, "the location needs the registry")
	})

	t.Run("an invalid locked version is an error, not a problem", func(t *testing.T) {
		f := newPlanFixture(t)
		_, err := planLocked(&f.planner, listOf(map[string]driverSpec{"alpha": {}}), v2Lock(lockedAlpha("not-a-version", linux)).V2)
		require.Error(t, err)
		var lockedErr *lockedSyncError
		assert.False(t, errors.As(err, &lockedErr))
	})

	t.Run("an undeclared host is reported with the mismatches", func(t *testing.T) {
		f := newPlanFixture(t)
		list := DriversList{Platforms: []string{"macos_arm64"}, Drivers: map[string]driverSpec{"alpha": {Version: constraint(t, ">=2")}}}
		lock := lockFileV2{Platforms: []resolution.Target{testSyncTarget(t, "macos_arm64")}, Drivers: []lockedDriver{lockedAlpha("1.0.0", macos)}}
		_, err := planLocked(&f.planner, list, lock)
		var lockedErr *lockedSyncError
		require.ErrorAs(t, err, &lockedErr)
		require.Len(t, lockedErr.mismatches, 2)
		assert.Contains(t, lockedErr.mismatches[1], "this platform (linux_amd64) is not in platforms of the driver list")
	})

	t.Run("an undeclared host is one problem among the trust problems", func(t *testing.T) {
		f := newPlanFixture(t)
		untrusted := lockedNamed("beta", "1.0.0", macos)
		untrusted.Signer.Fingerprint = "0000000000000000000000000000000000000000"
		list := DriversList{Platforms: []string{"macos_arm64"}, Drivers: map[string]driverSpec{"alpha": {}, "beta": {}}}
		lock := lockFileV2{
			Platforms: []resolution.Target{testSyncTarget(t, "macos_arm64")},
			Drivers:   []lockedDriver{lockedAlpha("1.0.0", macos), untrusted},
		}
		_, err := planLocked(&f.planner, list, lock)
		var lockedErr *lockedSyncError
		require.ErrorAs(t, err, &lockedErr)
		require.Len(t, lockedErr.mismatches, 1, "no driver is reported for lacking the undeclared host: %q", lockedErr.mismatches)
		assert.Contains(t, lockedErr.mismatches[0], "this platform (linux_amd64) is not in platforms of the driver list")
		require.Len(t, lockedErr.trust, 1)
		assert.Contains(t, lockedErr.trust[0], "driver beta")
	})
}

// noIndexBase is the test base model whose index accesses fail and are
// counted, for commands that must not fetch any index.
func noIndexBase(calls *int) baseModel {
	base := testBaseModel()
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		*calls++
		return nil, errors.New("the index must not be fetched")
	}
	base.getRegistryIndex = func(dbc.Registry) ([]dbc.Driver, error) {
		*calls++
		return nil, errors.New("the index must not be fetched")
	}
	return base
}

func (suite *SubcommandTestSuite) TestSyncLocked() {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")
	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\nversion = '=1.0.0'\n"), 0o644))
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(testBaseModel()))

	// A comment that dbc does not write shows that the file is not even
	// rewritten with the same entries.
	written, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	lockContents := append([]byte("# kept as it is\n"), written...)
	suite.Require().NoError(os.WriteFile(lockPath, lockContents, 0o644))

	calls := 0
	m := SyncCmd{Path: listPath, Level: suite.configLevel, Locked: true}.GetModelCustom(noIndexBase(&calls))
	suite.validateOutput("✓ test-driver-1-1.0.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.driverIsInstalledWithVersion("test-driver-1", "1.0.0", true)
	suite.Zero(calls)

	m = SyncCmd{Path: listPath, Level: suite.configLevel, Locked: true}.GetModelCustom(noIndexBase(&calls))
	suite.validateOutput("✓ test-driver-1-1.0.0 already installed\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.Zero(calls)

	actual, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(lockContents, actual)
}

func (suite *SubcommandTestSuite) TestSyncLockedFailsWithoutChanges() {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")

	suite.Run("without a lock file", func() {
		suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
		calls := 0
		out := suite.runCmdErr(SyncCmd{Path: listPath, Locked: true}.GetModelCustom(noIndexBase(&calls)))
		suite.Contains(out, "dbc sync --locked needs a lock file")
		suite.NoFileExists(lockPath)
		suite.Zero(calls)
	})

	suite.Run("with a version 1 lock file", func() {
		v1 := []byte("version = 1\n\n[[drivers]]\nname = 'test-driver-1'\nversion = '1.0.0'\nplatform = 'linux_amd64'\n")
		suite.Require().NoError(os.WriteFile(lockPath, v1, 0o644))
		calls := 0
		out := suite.runCmdErr(SyncCmd{Path: listPath, Locked: true}.GetModelCustom(noIndexBase(&calls)))
		suite.Contains(out, "needs a version 2 lock file")
		actual, err := os.ReadFile(lockPath)
		suite.Require().NoError(err)
		suite.Equal(v1, actual)
		suite.Zero(calls)
	})

	suite.Run("with every mismatch at once", func() {
		suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\nversion = '=1.0.0'\n"), 0o644))
		suite.Require().NoError(os.Remove(lockPath))
		suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(testBaseModel()))
		lockContents, err := os.ReadFile(lockPath)
		suite.Require().NoError(err)

		suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\nversion = '>=1.1.0'\n[drivers.test-driver-2]\n"), 0o644))
		calls, downloads := 0, 0
		base := noIndexBase(&calls)
		base.downloadPkg = func(pkg dbc.PkgInfo) (*os.File, error) {
			downloads++
			return downloadTestPkg(pkg)
		}
		out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel, Locked: true}.GetModelCustom(base))
		suite.Contains(out, "driver test-driver-1: version 1.0.0 in the lock file does not satisfy >=1.1.0")
		suite.Contains(out, "driver test-driver-2 is in the driver list but not in the lock file")
		suite.Contains(out, "run `dbc lock`")
		actual, err := os.ReadFile(lockPath)
		suite.Require().NoError(err)
		suite.Equal(lockContents, actual)
		suite.Zero(calls)
		suite.Zero(downloads)
		suite.driverIsNotInstalled("test-driver-1")
	})
}

func (suite *SubcommandTestSuite) TestSyncLockedChecksumMismatch() {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")
	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\nversion = '=1.0.0'\n"), 0o644))
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(testBaseModel()))
	written, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	hash := regexp.MustCompile(`sha256:[0-9a-f]{64}`)
	suite.Require().True(hash.Match(written))
	lockContents := hash.ReplaceAll(written, []byte("sha256:"+strings.Repeat("0", 64)))
	suite.Require().NoError(os.WriteFile(lockPath, lockContents, 0o644))

	calls, downloads := 0, 0
	base := noIndexBase(&calls)
	base.downloadPkg = func(pkg dbc.PkgInfo) (*os.File, error) {
		downloads++
		return downloadTestPkg(pkg)
	}
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel, Locked: true}.GetModelCustom(base))
	suite.Contains(out, "test-driver-1")
	suite.Equal(1, downloads)
	suite.Zero(calls)
	suite.driverIsNotInstalled("test-driver-1")
	actual, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(lockContents, actual)
}

// assertSignedLockedSync locks test-driver-no-sig from a registry with
// policy and installs it with dbc sync --locked without any index. Once the
// registry is configured with changed, dbc sync --locked fails before any
// download, also with --no-verify, and reports a trust problem.
func (suite *SubcommandTestSuite) assertSignedLockedSync(policy, changed dbc.RegistrySigningPolicy, archive string) {
	listPath := suite.writeDriverList("test-driver-no-sig")
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(signedBase(policy, archive)))
	lock := suite.readLockBytes()

	var replay syncCounters
	out := suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Locked: true, Json: true}.GetModelCustom(replay.counted(signedBase(policy, archive))))
	suite.driverIsInstalled("test-driver-no-sig", false)
	suite.Contains(out, fmt.Sprintf(`"signature_verified":%t`, !policy.IsUnsigned()))
	suite.Zero(replay.index)
	suite.Zero(replay.boundIndex)
	suite.Equal(lock, suite.readLockBytes())

	suite.Require().NoError(config.UninstallDriver(getConfig(suite.configLevel), suite.getInstalledDriver("test-driver-no-sig")))
	for _, noVerify := range []bool{false, true} {
		var rejected syncCounters
		cmd := SyncCmd{Path: listPath, Level: suite.configLevel, Locked: true, NoVerify: noVerify}
		out := suite.runCmdErr(cmd.GetModelCustom(rejected.counted(signedBase(changed, archive))))
		suite.Contains(out, "check the registries and signing keys", "noVerify=%t", noVerify)
		suite.Zero(rejected.downloads, "noVerify=%t", noVerify)
		suite.Zero(rejected.index, "noVerify=%t", noVerify)
		suite.Zero(rejected.boundIndex, "noVerify=%t", noVerify)
		suite.Equal(lock, suite.readLockBytes(), "the lock file must stay untouched")
		suite.driverIsNotInstalled("test-driver-no-sig")
	}
}

func (suite *SubcommandTestSuite) TestSyncLockedWithCustomSigningKey() {
	archive, key := suite.signedArchiveFile()
	_, other := suite.signedArchiveFile()
	suite.Run("another key", func() {
		suite.assertSignedLockedSync(dbc.SignWith(key), dbc.SignWith(other), archive)
	})
	suite.Run("the Columnar key", func() {
		suite.assertSignedLockedSync(dbc.SignWith(key), dbc.RegistrySigningPolicy{}, archive)
	})
	suite.Run("unsigned", func() {
		suite.assertSignedLockedSync(dbc.SignWith(key), dbc.Unsigned(), archive)
	})
}

func (suite *SubcommandTestSuite) TestSyncLockedWithUnsignedRegistry() {
	archive, key := suite.signedArchiveFile()
	suite.assertSignedLockedSync(dbc.Unsigned(), dbc.SignWith(key), archive)
}

// TestSyncLockedVerifiesWithTheCertificateOfTheSignerCheck replaces the key
// file after dbc sync --locked checked the pinned signer, before the package
// is downloaded: the package is still verified with the certificate that
// the check read.
func (suite *SubcommandTestSuite) TestSyncLockedVerifiesWithTheCertificateOfTheSignerCheck() {
	archive, key, armored := suite.signedArchive()
	keyPath := filepath.Join(suite.T().TempDir(), "acme.asc")
	suite.Require().NoError(os.WriteFile(keyPath, []byte(armored), 0o600))
	client, err := dbc.NewClient(dbc.WithGlobalConfig(&dbc.GlobalConfig{
		Registries:               []dbc.RegistryEntry{{URL: testRegistry.BaseURL.String(), SigningKey: "acme"}},
		SigningKeys:              map[string]dbc.SigningKeyDefinition{"acme": {Type: dbc.SigningKeyTypeOpenPGP, PublicKeyFile: &keyPath}},
		ExcludeBuiltinRegistries: true,
	}))
	suite.Require().NoError(err)
	configured := client.Registries()
	suite.Require().Len(configured, 1)
	policy := configured[0].Signing

	listPath := suite.writeDriverList("test-driver-no-sig")
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(signedBase(policy, archive)))
	suite.Equal(key.Fingerprint(), suite.lockedDriverEntry("test-driver-no-sig").Signer.Fingerprint)

	_, _, replacement := suite.signedArchive()
	base := signedBase(policy, archive)
	download := base.downloadPkg
	base.downloadPkg = func(pkg dbc.PkgInfo) (*os.File, error) {
		suite.Require().NoError(os.WriteFile(keyPath, []byte(replacement), 0o600))
		return download(pkg)
	}
	out := suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Locked: true, Json: true}.GetModelCustom(base))
	suite.Contains(out, `"signature_verified":true`)
	suite.driverIsInstalled("test-driver-no-sig", false)
}
