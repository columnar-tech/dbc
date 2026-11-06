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
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/auth"
	"github.com/columnar-tech/dbc/config"
	"github.com/go-faster/yaml"
)

const otherTarget = "plan9_riscv"

// syncCounters counts the registry and download accesses of a sync run.
type syncCounters struct {
	mu                           sync.Mutex
	index, boundIndex, downloads int
	downloadDirs                 []string
	downloadFiles                []string
}

// counted wraps base so that every registry and download access is counted.
func (c *syncCounters) counted(base baseModel) baseModel {
	index, boundIndex, download := base.getDriverRegistry, base.getRegistryIndex, base.downloadPkg
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		c.mu.Lock()
		c.index++
		c.mu.Unlock()
		return index()
	}
	base.getRegistryIndex = func(r dbc.Registry) ([]dbc.Driver, error) {
		c.mu.Lock()
		c.boundIndex++
		c.mu.Unlock()
		return boundIndex(r)
	}
	base.downloadPkg = func(pkg dbc.PkgInfo) (*os.File, error) {
		c.mu.Lock()
		c.downloads++
		c.mu.Unlock()
		f, err := download(pkg)
		c.mu.Lock()
		defer c.mu.Unlock()
		if f != nil {
			c.downloadDirs = append(c.downloadDirs, filepath.Dir(f.Name()))
			c.downloadFiles = append(c.downloadFiles, f.Name())
		}
		return f, err
	}
	return base
}

// unreachableRegistries makes every registry index access fail.
func unreachableRegistries(base baseModel) baseModel {
	base.getDriverRegistry = func() ([]dbc.Driver, error) { return nil, fmt.Errorf("index must not be fetched") }
	base.getRegistryIndex = func(dbc.Registry) ([]dbc.Driver, error) { return nil, fmt.Errorf("index must not be fetched") }
	return base
}

func decodeTestIndex(index string) ([]dbc.Driver, error) {
	var decoded struct {
		Drivers []dbc.Driver `yaml:"drivers"`
	}
	if err := yaml.Unmarshal([]byte(index), &decoded); err != nil {
		return nil, err
	}
	for i := range decoded.Drivers {
		decoded.Drivers[i].Registry = &testRegistry
	}
	return decoded.Drivers, nil
}

// multiTargetBase publishes test-driver-1 v1.0.0 for the current target with
// the given hash and for another target with a fixed one.
func multiTargetBase(hash string) baseModel {
	base := testBaseModel()
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
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
            hash: sha256:%s
`, config.PlatformTuple(), hash, otherTarget, strings.Repeat("a", 64)))
	}
	return base
}

func (suite *SubcommandTestSuite) lockPath() string { return filepath.Join(suite.tempdir, "dbc.lock") }

func (suite *SubcommandTestSuite) writeDriverList(drivers ...string) string {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	var b strings.Builder
	b.WriteString("[drivers]\n")
	for _, d := range drivers {
		fmt.Fprintf(&b, "[drivers.%s]\n", d)
	}
	suite.Require().NoError(os.WriteFile(listPath, []byte(b.String()), 0o644))
	return listPath
}

func (suite *SubcommandTestSuite) readV2Lock() lockFileV2 {
	parsed, err := readLockFile(suite.lockPath())
	suite.Require().NoError(err)
	suite.Require().Equal(lockFileVersionV2, parsed.Version)
	return parsed.V2
}

func (suite *SubcommandTestSuite) lockedDriverEntry(driver string) lockedDriver {
	for _, d := range suite.readV2Lock().Drivers {
		if d.Name == driver {
			return d
		}
	}
	suite.FailNow("driver not in lock file", driver)
	return lockedDriver{}
}

// assertLockedArchive checks the version and the current-target hash recorded
// for driver in the v2 lock file.
func (suite *SubcommandTestSuite) assertLockedArchive(driver, version, archive string) {
	suite.T().Helper()
	d := suite.lockedDriverEntry(driver)
	suite.Equal(version, d.Version)
	for _, a := range d.Artifacts {
		if a.Target == config.PlatformTuple() {
			suite.Equal(lockHashPrefix+testArchiveSHA256(suite, archive), a.Hash)
			return
		}
	}
	suite.Fail("no artifact for the current target", driver)
}

func (suite *SubcommandTestSuite) writeV1Lock(version, platform, checksum string) []byte {
	contents := []byte(fmt.Sprintf("version = 1\n[[drivers]]\nname = 'test-driver-1'\nversion = '%s'\nplatform = '%s'\nchecksum = '%s'\n",
		version, platform, checksum))
	suite.Require().NoError(os.WriteFile(suite.lockPath(), contents, 0o644))
	return contents
}

func (suite *SubcommandTestSuite) libraryChecksum(archive string) string {
	f, err := os.Open(filepath.Join("testdata", archive))
	suite.Require().NoError(err)
	defer f.Close()
	info, err := config.InspectPackageArchive(f)
	suite.Require().NoError(err)
	return info.LibrarySHA256
}

func captureStderr(suite *SubcommandTestSuite, fn func()) string {
	r, w, err := os.Pipe()
	suite.Require().NoError(err)
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	suite.Require().NoError(w.Close())
	data, err := io.ReadAll(r)
	suite.Require().NoError(err)
	return string(data)
}

func (suite *SubcommandTestSuite) supportsReuse() bool {
	return runtime.GOOS != "js"
}

func (suite *SubcommandTestSuite) TestSyncWritesV2LockWithAllPublishedTargets() {
	listPath := suite.writeDriverList("test-driver-1")
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(multiTargetBase(hash)))

	d := suite.lockedDriverEntry("test-driver-1")
	suite.Equal("1.0.0", d.Version)
	suite.Equal(lockedSource{Type: lockSourceRegistry, URL: "https://registry.columnar.tech"}, d.Source)
	suite.Require().Len(d.Artifacts, 2)
	hashes := map[string]string{}
	for _, a := range d.Artifacts {
		suite.NotNil(a.Path)
		hashes[a.Target] = a.Hash
	}
	suite.Equal(map[string]string{
		config.PlatformTuple(): lockHashPrefix + hash,
		otherTarget:            lockHashPrefix + strings.Repeat("a", 64),
	}, hashes)
}

func (suite *SubcommandTestSuite) TestSyncMeasuresHashWhenRegistryPublishesNone() {
	listPath := suite.writeDriverList("test-driver-1")
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(testBaseModel()))
	suite.assertLockedArchive("test-driver-1", "1.1.0", "test-driver-1.1.tar.gz")
	suite.Len(suite.lockedDriverEntry("test-driver-1").Artifacts, 1, "unpublished hashes of other targets are not recorded")
}

func (suite *SubcommandTestSuite) TestSyncSecondRunNeedsNeitherIndexNorDownload() {
	listPath := suite.writeDriverList("test-driver-1")
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(multiTargetBase(hash)))

	var counters syncCounters
	base := counters.counted(unreachableRegistries(multiTargetBase(hash)))
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(base))
	suite.Zero(counters.index)
	suite.Zero(counters.boundIndex)
	if suite.supportsReuse() {
		suite.Zero(counters.downloads)
	}
	suite.driverIsInstalledWithVersion("test-driver-1", "1.0.0", true)
}

func (suite *SubcommandTestSuite) TestSyncCompletesLockedDriverFromBoundRegistryOnly() {
	listPath := suite.writeDriverList("test-driver-1")
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	otherPath := "test-driver-1/1.0.0/test-driver-1-other.tar.gz"
	suite.Require().NoError(writeLockFileV2(suite.lockPath(), lockFileV2{Version: lockFileVersionV2, Drivers: []lockedDriver{{
		Name:    "test-driver-1",
		Version: "1.0.0",
		Source:  lockedSource{Type: lockSourceRegistry, URL: "https://registry.columnar.tech"},
		Artifacts: []lockedArtifact{
			{Target: otherTarget, Path: &otherPath, Hash: lockHashPrefix + strings.Repeat("a", 64)},
		},
	}}}))

	var counters syncCounters
	base := multiTargetBase(hash)
	publish := base.getDriverRegistry
	base.getRegistryIndex = func(dbc.Registry) ([]dbc.Driver, error) { return publish() }
	base.getDriverRegistry = func() ([]dbc.Driver, error) { return nil, fmt.Errorf("merged index must not be fetched") }
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(counters.counted(base)))

	suite.Equal(1, counters.boundIndex)
	suite.Zero(counters.index)
	d := suite.lockedDriverEntry("test-driver-1")
	suite.Len(d.Artifacts, 2)
	suite.assertLockedArchive("test-driver-1", "1.0.0", "test-driver-1.tar.gz")
}

func (suite *SubcommandTestSuite) TestSyncMigratesV1LockOfSamePlatformWhenNotInstalled() {
	listPath := suite.writeDriverList("test-driver-1")
	suite.writeV1Lock("1.0.0", config.PlatformTuple(), suite.libraryChecksum("test-driver-1.tar.gz"))

	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(testBaseModel()))
	suite.driverIsInstalledWithVersion("test-driver-1", "1.0.0", true)
	suite.assertLockedArchive("test-driver-1", "1.0.0", "test-driver-1.tar.gz")
}

func (suite *SubcommandTestSuite) TestSyncMigratesV1LockOfSamePlatformWhenInstalled() {
	listPath := suite.writeDriverList("test-driver-1")
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(hashedBaseModel(hash)))
	suite.Require().NoError(os.Remove(suite.lockPath()))
	suite.writeV1Lock("1.0.0", config.PlatformTuple(), suite.libraryChecksum("test-driver-1.tar.gz"))

	var counters syncCounters
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(counters.counted(hashedBaseModel(hash))))
	if suite.supportsReuse() {
		suite.Zero(counters.downloads)
	}
	suite.assertLockedArchive("test-driver-1", "1.0.0", "test-driver-1.tar.gz")
}

// assertV1ChecksumMismatch syncs with a v1 lock file whose checksum does not
// match and expects a checksum_failed error that changes neither the lock file
// nor an existing installation.
func (suite *SubcommandTestSuite) assertV1ChecksumMismatch(cmd SyncCmd, installed bool) {
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	cmd.Path, cmd.Level = suite.writeDriverList("test-driver-1"), suite.configLevel

	var oldLibrary []byte
	if installed {
		suite.runCmd(SyncCmd{Path: cmd.Path, Level: suite.configLevel}.GetModelCustom(hashedBaseModel(hash)))
		var err error
		oldLibrary, err = os.ReadFile(suite.getInstalledDriver("test-driver-1").Driver.Shared.Get(config.PlatformTuple()))
		suite.Require().NoError(err)
	}
	lock := suite.writeV1Lock("1.0.0", config.PlatformTuple(), strings.Repeat("0", 64))

	out := suite.runCmdErr(cmd.GetModelCustom(hashedBaseModel(hash)))
	suite.assertJSONErrorEnvelope(out, "checksum_failed", "checksum mismatch")

	current, err := os.ReadFile(suite.lockPath())
	suite.Require().NoError(err)
	suite.Equal(lock, current, "the lock file must stay untouched")
	if installed {
		library, err := os.ReadFile(suite.getInstalledDriver("test-driver-1").Driver.Shared.Get(config.PlatformTuple()))
		suite.Require().NoError(err)
		suite.Equal(oldLibrary, library)
	} else {
		suite.driverIsNotInstalled("test-driver-1")
	}
}

func (suite *SubcommandTestSuite) TestSyncV1ChecksumMismatchJSON() {
	suite.assertV1ChecksumMismatch(SyncCmd{Json: true}, false)
}

func (suite *SubcommandTestSuite) TestSyncV1ChecksumMismatchJSONStream() {
	suite.assertV1ChecksumMismatch(SyncCmd{JsonStreamProgress: true}, false)
}

func (suite *SubcommandTestSuite) TestSyncV1ChecksumMismatchOfInstalledDriverJSON() {
	suite.assertV1ChecksumMismatch(SyncCmd{Json: true}, true)
}

func (suite *SubcommandTestSuite) TestSyncV1ChecksumMismatchOfInstalledDriverJSONStream() {
	suite.assertV1ChecksumMismatch(SyncCmd{JsonStreamProgress: true}, true)
}

func (suite *SubcommandTestSuite) TestSyncArchiveHashMismatchIsChecksumFailedJSON() {
	listPath := suite.writeDriverList("test-driver-1")
	for _, cmd := range []SyncCmd{{Json: true}, {JsonStreamProgress: true}} {
		cmd.Path, cmd.Level = listPath, suite.configLevel
		out := suite.runCmdErr(cmd.GetModelCustom(hashedBaseModel(strings.Repeat("0", 64))))
		suite.assertJSONErrorEnvelope(out, "checksum_failed", "archive SHA-256 mismatch")
		suite.NoFileExists(suite.lockPath())
		suite.driverIsNotInstalled("test-driver-1")
	}
}

func (suite *SubcommandTestSuite) TestSyncV1LockOfOtherPlatformWarns() {
	for name, platform := range map[string]string{"other": "plan9_riscv", "empty": ""} {
		suite.Run(name, func() {
			listPath := suite.writeDriverList("test-driver-1")
			suite.writeV1Lock("1.0.0", platform, strings.Repeat("0", 64))
			var out string
			stderr := captureStderr(suite, func() {
				out = suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(testBaseModel()))
			})
			suite.Contains(stderr, "warning")
			suite.Contains(stderr, "test-driver-1")
			if platform == "" {
				suite.Contains(stderr, "unknown")
			} else {
				suite.Contains(stderr, platform)
			}
			suite.NotContains(out, "warning")
			suite.driverIsInstalledWithVersion("test-driver-1", "1.0.0", true)
			suite.assertLockedArchive("test-driver-1", "1.0.0", "test-driver-1.tar.gz")
			suite.Require().NoError(os.Remove(suite.lockPath()))
		})
	}
}

func (suite *SubcommandTestSuite) TestSyncUnverifiedInstallIsReinstalledWithVerification() {
	listPath := suite.writeDriverList("test-driver-1")
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, NoVerify: true}.GetModelCustom(hashedBaseModel(hash)))
	suite.assertReceipt("test-driver-1", "test-driver-1.tar.gz", false)

	var counters syncCounters
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(counters.counted(hashedBaseModel(hash))))
	suite.Equal(1, counters.downloads)
	suite.assertReceipt("test-driver-1", "test-driver-1.tar.gz", true)
}

func (suite *SubcommandTestSuite) TestSyncNoVerifyStillEnforcesLockedArchiveHash() {
	listPath := suite.writeDriverList("test-driver-1")
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(hashedBaseModel(hash)))
	suite.Require().NoError(config.UninstallDriver(getConfig(suite.configLevel), suite.getInstalledDriver("test-driver-1")))

	// The locked hash is replayed, so even a registry that now publishes a
	// different one cannot change what is accepted.
	wrong := testBaseModel()
	wrong.downloadPkg = func(dbc.PkgInfo) (*os.File, error) { return openTestdataCopy("test-driver-1.1.tar.gz") }
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel, NoVerify: true}.GetModelCustom(wrong))
	suite.Contains(out, "archive SHA-256 mismatch")
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestSyncReverifiesInstalledSignature() {
	listPath := suite.writeDriverList("test-driver-1")
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(hashedBaseModel(hash)))
	installed := suite.getInstalledDriver("test-driver-1")
	library := installed.Driver.Shared.Get(config.PlatformTuple())
	suite.Require().NoError(os.WriteFile(library+".sig", []byte("invalid signature"), 0o600))

	// The receipt still says signature_verified=true, but the current bytes
	// must be checked again rather than trusting that historical flag.
	var counters syncCounters
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(counters.counted(hashedBaseModel(hash))))
	suite.Equal(1, counters.downloads)
	suite.assertReceipt("test-driver-1", "test-driver-1.tar.gz", true)
}

func (suite *SubcommandTestSuite) TestSyncInstallFailureKeepsWrittenLock() {
	listPath := suite.writeDriverList("test-driver-no-sig")
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(testBaseModel()))
	suite.Contains(out, "failed to verify signature")
	suite.assertLockedArchive("test-driver-no-sig", "1.0.0", "test-driver-no-sig.tar.gz")
	suite.driverIsNotInstalled("test-driver-no-sig")
}

func (suite *SubcommandTestSuite) TestSyncPlanningFailureLeavesEveryDriverUntouched() {
	listPath := suite.writeDriverList("test-driver-1", "test-driver-2")
	base := testBaseModel()
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		return decodeTestIndex(fmt.Sprintf(`drivers:
  - name: Test Driver 1
    path: test-driver-1
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: %[1]s
            url: test-driver-1/1.0.0/test-driver-1.tar.gz
  - name: Test Driver 2
    path: test-driver-2
    pkginfo:
      - version: v2.0.0
        packages:
          - platform: %[1]s
            url: test-driver-2/2.0.0/test-driver-2.tar.gz
          - platform: %[2]s
            url: test-driver-2/2.0.0/other.tar.gz
            hash: sha256:nothex
`, config.PlatformTuple(), otherTarget))
	}
	var counters syncCounters
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(counters.counted(base)))
	suite.Contains(out, "invalid archive hash")
	suite.Zero(counters.downloads)
	suite.NoFileExists(suite.lockPath())
	suite.driverIsNotInstalled("test-driver-1")
	suite.driverIsNotInstalled("test-driver-2")
}

func (suite *SubcommandTestSuite) TestSyncDownloadFailureLeavesEveryDriverUntouched() {
	listPath := suite.writeDriverList("test-driver-1", "test-driver-2")
	var counters syncCounters
	base := testBaseModel()
	download := base.downloadPkg
	base.downloadPkg = func(pkg dbc.PkgInfo) (*os.File, error) {
		if pkg.Driver.Path == "test-driver-2" {
			return nil, fmt.Errorf("download refused")
		}
		return download(pkg)
	}
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(counters.counted(base)))
	suite.Contains(out, "download refused")
	suite.NoFileExists(suite.lockPath())
	suite.driverIsNotInstalled("test-driver-1")
	for _, dir := range counters.downloadDirs {
		suite.NoDirExists(dir, "the archive of the driver that was not installed must be removed")
	}
}

func (suite *SubcommandTestSuite) TestSyncRemovesDownloadedArchives() {
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	for name, run := range map[string]func(*syncCounters) bool{
		"success": func(c *syncCounters) bool {
			suite.runCmd(SyncCmd{Path: suite.writeDriverList("test-driver-1"), Level: suite.configLevel}.GetModelCustom(c.counted(hashedBaseModel(hash))))
			return true
		},
		"hash mismatch": func(c *syncCounters) bool {
			suite.runCmdErr(SyncCmd{Path: suite.writeDriverList("test-driver-1"), Level: suite.configLevel}.GetModelCustom(c.counted(hashedBaseModel(strings.Repeat("0", 64)))))
			return true
		},
		"signature failure": func(c *syncCounters) bool {
			suite.runCmdErr(SyncCmd{Path: suite.writeDriverList("test-driver-no-sig"), Level: suite.configLevel}.GetModelCustom(c.counted(testBaseModel())))
			return true
		},
	} {
		suite.Run(name, func() {
			_ = os.Remove(suite.lockPath())
			var counters syncCounters
			run(&counters)
			suite.Require().NotEmpty(counters.downloadDirs)
			for _, dir := range counters.downloadDirs {
				suite.NoDirExists(dir)
			}
		})
	}
}

// registryServer serves the test archive below /base/ and records the request
// details the credential tests inspect.
type registryServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
	status   int
}

type recordedRequest struct {
	Path, Authorization, Token string
	User                       string
	Password                   string
}

func newRegistryServer(archive []byte) *registryServer {
	s := &registryServer{status: http.StatusOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, _ := r.BasicAuth()
		s.mu.Lock()
		s.requests = append(s.requests, recordedRequest{
			Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), Token: r.URL.Query().Get("token"),
			User: user, Password: password,
		})
		status := s.status
		s.mu.Unlock()
		if status != http.StatusOK {
			http.Error(w, "failing", status)
			return
		}
		_, _ = w.Write(archive)
	}))
	return s
}

func (s *registryServer) recorded() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

// useCredentials stores a bearer token for host in a private credential file.
func (suite *SubcommandTestSuite) useCredentials(hostURL, token string) {
	credPath := filepath.Join(suite.T().TempDir(), "credentials.toml")
	suite.Require().NoError(os.WriteFile(credPath, []byte(fmt.Sprintf(
		"[[credentials]]\ntype = 'oauth'\nauth_uri = '%[1]s/auth'\nregistry_url = '%[1]s'\ntoken = '%[2]s'\n", hostURL, token)), 0o600))
	restore := auth.SetCredPathForTesting(credPath)
	auth.ResetCredentialsForTesting()
	suite.T().Cleanup(func() {
		restore()
		auth.ResetCredentialsForTesting()
	})
}

func readTestArchive(suite *SubcommandTestSuite, name string) []byte {
	data, err := os.ReadFile(filepath.Join("testdata", name))
	suite.Require().NoError(err)
	return data
}

func (suite *SubcommandTestSuite) TestSyncReplayDoesNotSendCredentialsToOtherHosts() {
	archive := readTestArchive(suite, "test-driver-1.tar.gz")
	registry := newRegistryServer(archive)
	defer registry.Close()
	mirror := newRegistryServer(archive)
	defer mirror.Close()
	suite.useCredentials(registry.URL, "REGISTRYTOKEN")

	listPath := suite.writeDriverList("test-driver-1")
	mirrorURL := mirror.URL + "/mirror/test-driver-1.tar.gz"
	suite.Require().NoError(writeLockFileV2(suite.lockPath(), lockFileV2{Version: lockFileVersionV2, Drivers: []lockedDriver{{
		Name:    "test-driver-1",
		Version: "1.0.0",
		Source:  lockedSource{Type: lockSourceRegistry, URL: registry.URL},
		Artifacts: []lockedArtifact{
			{Target: config.PlatformTuple(), URL: &mirrorURL, Hash: lockHashPrefix + testArchiveSHA256(suite, "test-driver-1.tar.gz")},
		},
	}}}))

	registryURL := must(url.Parse(registry.URL))
	base := baseModel{
		downloadPkg:       downloadPkg,
		getDriverRegistry: func() ([]dbc.Driver, error) { return nil, fmt.Errorf("index must not be fetched") },
		getRegistries:     func() ([]dbc.Registry, error) { return []dbc.Registry{{BaseURL: registryURL}}, nil },
		getRegistryIndex:  func(dbc.Registry) ([]dbc.Driver, error) { return nil, fmt.Errorf("index must not be fetched") },
	}
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(base))

	suite.Empty(registry.recorded(), "the registry host is not contacted for a replay of a mirrored artifact")
	got := mirror.recorded()
	suite.Require().Len(got, 1)
	suite.Empty(got[0].Authorization, "the registry's credential must not reach another host")
	suite.driverIsInstalledWithVersion("test-driver-1", "1.0.0", true)
}

func (suite *SubcommandTestSuite) TestSyncKeepsRegistrySecretsOutOfLockOutputAndErrors() {
	archive := readTestArchive(suite, "test-driver-1.tar.gz")
	server := newRegistryServer(archive)
	defer server.Close()
	suite.useCredentials(server.URL, "REGISTRYTOKEN")

	base := must(url.Parse(server.URL))
	base.User = url.UserPassword("user", "SUPERSECRET")
	base.Path = "/base"
	base.RawQuery = "token=SUPERSECRET"
	registry := &dbc.Registry{BaseURL: base}
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")

	model := baseModel{
		downloadPkg: downloadPkg,
		getDriverRegistry: func() ([]dbc.Driver, error) {
			drivers, err := decodeTestIndex(fmt.Sprintf(`drivers:
  - name: Test Driver 1
    path: test-driver-1
    pkginfo:
      - version: v1.0.0
        packages:
          - platform: %s
            url: test-driver-1/1.0.0/test-driver-1.tar.gz
            hash: sha256:%s
`, config.PlatformTuple(), hash))
			for i := range drivers {
				drivers[i].Registry = registry
			}
			return drivers, err
		},
		getRegistries:    func() ([]dbc.Registry, error) { return []dbc.Registry{*registry}, nil },
		getRegistryIndex: func(dbc.Registry) ([]dbc.Driver, error) { return nil, fmt.Errorf("index must not be fetched") },
	}

	var counters syncCounters
	model = counters.counted(model)
	listPath := suite.writeDriverList("test-driver-1")
	first := suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, JsonStreamProgress: true}.GetModelCustom(model))
	suite.Require().Len(counters.downloadFiles, 1)
	name := filepath.Base(counters.downloadFiles[0])
	suite.NotContains(name, "?", "download names must not contain the URL query")
	suite.NotContains(name, "SUPERSECRET", "download names must not contain registry secrets")
	lock, err := os.ReadFile(suite.lockPath())
	suite.Require().NoError(err)
	suite.NotContains(string(lock), "SUPERSECRET")
	suite.NotContains(first, "SUPERSECRET")
	suite.Contains(string(lock), server.URL[len("http://"):]+"/base", "the lock names the registry without credentials")

	// Replay into a fresh installation so the archive is downloaded again with
	// the rebound registry URL, then once more against a failing registry.
	suite.T().Setenv("ADBC_DRIVER_PATH", filepath.Join(suite.tempdir, "second"))
	replay := model
	replay.getDriverRegistry = func() ([]dbc.Driver, error) { return nil, fmt.Errorf("index must not be fetched") }
	second := suite.runCmd(SyncCmd{Path: listPath, Level: config.ConfigEnv, Json: true}.GetModelCustom(replay))
	suite.NotContains(second, "SUPERSECRET")

	suite.T().Setenv("ADBC_DRIVER_PATH", filepath.Join(suite.tempdir, "third"))
	server.mu.Lock()
	server.status = http.StatusNotFound
	server.mu.Unlock()
	failed := suite.runCmdErr(SyncCmd{Path: listPath, Level: config.ConfigEnv, Json: true}.GetModelCustom(replay))
	suite.assertJSONErrorEnvelope(failed, "sync_failed", "failed to download driver")
	suite.NotContains(failed, "SUPERSECRET")

	requests := server.recorded()
	suite.Require().GreaterOrEqual(len(requests), 3)
	for _, r := range requests {
		suite.Equal("SUPERSECRET", r.Token, "the rebound registry query is applied")
		suite.Equal("Bearer REGISTRYTOKEN", r.Authorization)
		suite.True(strings.HasPrefix(r.Path, "/base/"), r.Path)
	}
}

// failingIndexServer answers every request with a server error.
func failingIndexServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "index unavailable", http.StatusInternalServerError)
	}))
}

// secretRegistryURL returns the URL of server with percent-encoded userinfo
// and a percent-encoded query value, as users may configure it.
func secretRegistryURL(server *httptest.Server, withQuery bool) *url.URL {
	u := must(url.Parse(server.URL))
	u.User = url.UserPassword("us@er", "p/w SECRETPASS")
	u.Path = "/base"
	if withQuery {
		u.RawQuery = "token=abc%2FSECRETTOKEN&token=SECRETTWO"
	}
	return u
}

func (suite *SubcommandTestSuite) assertNoSecrets(text string) {
	suite.T().Helper()
	for _, secret := range []string{"SECRETPASS", "SECRETTOKEN", "SECRETTWO", "SECRETUSER", "us@er", "us%40er", "p%2Fw", "p/w"} {
		suite.NotContains(text, secret)
	}
}

func (suite *SubcommandTestSuite) TestSyncFirstResolutionErrorsHideRegistrySecrets() {
	listPath := suite.writeDriverList("test-driver-1")
	failing := failingIndexServer()
	defer failing.Close()
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("drivers:\n  - name: Other\n    path: other-driver\n"))
	}))
	defer working.Close()

	failingURL := secretRegistryURL(failing, true)
	for name, registries := range map[string][]dbc.Registry{
		"every registry fails":   {{BaseURL: failingURL}},
		"driver not found among": {{BaseURL: failingURL}, {BaseURL: must(url.Parse(working.URL))}},
	} {
		suite.Run(name, func() {
			client, err := dbc.NewClient(dbc.WithRegistries(registries))
			suite.Require().NoError(err)
			base := testBaseModel()
			// The real client produces the error text, including the registry URL.
			base.getDriverRegistry = func() ([]dbc.Driver, error) { return client.Search(context.Background(), "") }
			base.getRegistries = func() ([]dbc.Registry, error) { return registries, nil }

			out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(base))
			suite.assertJSONErrorEnvelope(out, "sync_failed", "500")
			suite.assertNoSecrets(out)
			suite.NoFileExists(suite.lockPath())
		})
	}
}

func (suite *SubcommandTestSuite) TestSyncCompletionErrorsHideRegistrySecrets() {
	listPath := suite.writeDriverList("test-driver-1")
	failing := failingIndexServer()
	defer failing.Close()
	registryURL := secretRegistryURL(failing, false)
	identity, err := canonicalRegistryIdentity(registryURL)
	suite.Require().NoError(err)
	path := "test-driver-1/1.0.0/other.tar.gz"
	suite.Require().NoError(writeLockFileV2(suite.lockPath(), lockFileV2{Version: lockFileVersionV2, Drivers: []lockedDriver{{
		Name:      "test-driver-1",
		Version:   "1.0.0",
		Source:    lockedSource{Type: lockSourceRegistry, URL: identity},
		Artifacts: []lockedArtifact{{Target: otherTarget, Path: &path, Hash: lockHashPrefix + strings.Repeat("a", 64)}},
	}}}))

	base := testBaseModel()
	base.getRegistries = func() ([]dbc.Registry, error) { return []dbc.Registry{{BaseURL: registryURL}}, nil }
	// The real single-registry fetch produces the error text.
	base.getRegistryIndex = fetchRegistryIndex
	out := suite.runCmdErr(SyncCmd{Path: listPath, Level: suite.configLevel, Json: true}.GetModelCustom(base))
	suite.assertJSONErrorEnvelope(out, "sync_failed", "could not fetch the index", "500")
	suite.assertNoSecrets(out)
}

// quitKey is a Ctrl+C key press.
var quitKey = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}

// preparedSyncModel starts installation with a plan prepared by the test.
type preparedSyncModel struct {
	syncModel
	items []syncItem
}

func (s preparedSyncModel) Init() tea.Cmd {
	return func() tea.Msg { return s.items }
}

func (s preparedSyncModel) WithJSONWriter(w io.Writer) tea.Model {
	s.jsonOut = w
	return s
}

func (suite *SubcommandTestSuite) TestSyncProjectLockHeldThroughInstallation() {
	listPath := suite.writeDriverList("test-driver-1")
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	first := SyncCmd{Path: listPath, Level: suite.configLevel}.newModel(hashedBaseModel(hash))
	items, err := first.prepare()
	suite.Require().NoError(err)
	defer first.projectLock.release()
	defer func() {
		for _, item := range items {
			removeDownload(item.File)
		}
	}()

	second := SyncCmd{Path: listPath, Level: suite.configLevel}.newModel(hashedBaseModel(hash))
	defer second.projectLock.release()
	type result struct {
		items []syncItem
		err   error
	}
	finished := make(chan result, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		items, err := second.prepare()
		finished <- result{items, err}
	}()
	<-started
	select {
	case r := <-finished:
		for _, item := range r.items {
			removeDownload(item.File)
		}
		suite.FailNow("second sync prepared before the first installed its drivers", "%v", r.err)
	case <-time.After(200 * time.Millisecond):
	}

	suite.runCmd(preparedSyncModel{syncModel: first, items: items})
	select {
	case r := <-finished:
		suite.Require().NoError(r.err)
		suite.Require().Len(r.items, 1)
		suite.NotNil(r.items[0].Installed, "the second sync sees the first sync's completed installation")
		for _, item := range r.items {
			removeDownload(item.File)
		}
	case <-time.After(5 * time.Second):
		suite.FailNow("second sync did not acquire the lock after installation")
	}
}

func (suite *SubcommandTestSuite) TestSyncQuitDuringPrepareExitsPromptlyAndWorkerCleansUp() {
	listPath := suite.writeDriverList("test-driver-1", "test-driver-no-sig")
	var counters syncCounters
	base := testBaseModel()
	download := base.downloadPkg
	release := make(chan struct{})
	base.downloadPkg = func(pkg dbc.PkgInfo) (*os.File, error) {
		f, err := download(pkg)
		prog.Send(quitKey)
		// The blocking call only returns after the program has exited.
		<-release
		return f, err
	}

	var out bytes.Buffer
	m := SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(counters.counted(base))
	prog = tea.NewProgram(m, tea.WithInput(nil), tea.WithOutput(&out), tea.WithoutRenderer())
	finished := make(chan error, 1)
	go func() {
		_, err := prog.Run()
		finished <- err
	}()
	select {
	case err := <-finished:
		suite.Require().NoError(err)
	case <-time.After(5 * time.Second):
		close(release)
		suite.FailNow("the program must exit while the worker is blocked")
	}
	close(release)

	snapshot := func() (int, []string) {
		counters.mu.Lock()
		defer counters.mu.Unlock()
		return counters.downloads, append([]string(nil), counters.downloadDirs...)
	}
	suite.Eventually(func() bool {
		_, dirs := snapshot()
		if len(dirs) == 0 {
			return false
		}
		for _, dir := range dirs {
			if _, err := os.Stat(dir); err == nil {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond, "the worker removes its downloads")
	downloads, dirs := snapshot()
	suite.Equal(1, downloads, "the worker stops before the next download")
	suite.NotEmpty(dirs)
	suite.NoFileExists(suite.lockPath())
	suite.driverIsNotInstalled("test-driver-1")
	prog = nil

	// The project lock was released: another sync can run at once.
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel, NoVerify: true}.GetModelCustom(testBaseModel()))
}

func (suite *SubcommandTestSuite) TestSyncQuitDuringInstallKeepsArchiveInUse() {
	var files []*os.File
	items := make([]syncItem, 3)
	for i := range items {
		f, err := openTestdataCopy("test-driver-1.tar.gz")
		suite.Require().NoError(err)
		files = append(files, f)
		items[i] = syncItem{Name: fmt.Sprintf("driver-%d", i), File: f}
	}
	defer func() {
		for _, f := range files {
			removeDownload(f)
		}
	}()

	model, _ := SyncCmd{}.newModel(testBaseModel()).Update(items)
	model, cmd := model.Update(quitKey)
	suite.Require().NotNil(cmd)
	suite.IsType(tea.QuitMsg{}, cmd())
	suite.Error(model.(syncModel).ctx.Err(), "the worker is asked to stop")
	suite.FileExists(files[0].Name(), "the archive in use by the install worker is not touched")
	suite.NoFileExists(files[1].Name())
	suite.NoFileExists(files[2].Name())
}
