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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

func (suite *SubcommandTestSuite) readLockBytes() []byte {
	suite.T().Helper()
	data, err := os.ReadFile(suite.lockPath())
	suite.Require().NoError(err)
	return data
}

func (suite *SubcommandTestSuite) TestLockCreatesV2LockWithoutInstalling() {
	listPath := suite.writeDriverList("test-driver-1")
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	out := suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(multiTargetBase(hash)))

	suite.Contains(out, "Locked 1 driver to "+suite.lockPath())
	suite.Contains(out, "test-driver-1-1.0.0")
	suite.Len(suite.lockedDriverEntry("test-driver-1").Artifacts, 2, "all published targets are recorded")
	suite.assertLockedArchive("test-driver-1", "1.0.0", "test-driver-1.tar.gz")
	suite.driverIsNotInstalled("test-driver-1")
	suite.Equal([]string{"dbc.toml"}, suite.getFilesInTempDir(), "nothing but the lock file is written")
}

func (suite *SubcommandTestSuite) TestLockPathMayBeADirectory() {
	suite.writeDriverList("test-driver-1")
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	suite.runCmd(LockCmd{Path: suite.tempdir}.GetModelCustom(hashedBaseModel(hash)))
	suite.assertLockedArchive("test-driver-1", "1.0.0", "test-driver-1.tar.gz")
}

func (suite *SubcommandTestSuite) TestLockKeepsCompatiblePin() {
	listPath := suite.writeDriverList("test-driver-1")
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(hashedBaseModel(hash)))
	before := suite.readLockBytes()

	// The test registry also publishes 1.1.0, which must not replace the pin.
	var counters syncCounters
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(counters.counted(testBaseModel())))
	suite.assertLockedArchive("test-driver-1", "1.0.0", "test-driver-1.tar.gz")
	suite.Equal(before, suite.readLockBytes())
	suite.Zero(counters.downloads)
}

func (suite *SubcommandTestSuite) TestLockDoesNotDownloadPublishedHashes() {
	listPath := suite.writeDriverList("test-driver-1")
	var counters syncCounters
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(counters.counted(hashedBaseModel(strings.Repeat("0", 64)))))
	suite.Zero(counters.downloads, "published hashes are recorded as they are")
	d := suite.lockedDriverEntry("test-driver-1")
	suite.Require().Len(d.Artifacts, 1)
	suite.Equal(lockHashPrefix+strings.Repeat("0", 64), d.Artifacts[0].Hash)
}

func (suite *SubcommandTestSuite) TestLockMeasuresUnpublishedHash() {
	listPath := suite.writeDriverList("test-driver-1")
	var counters syncCounters
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(counters.counted(testBaseModel())))
	suite.Equal(1, counters.downloads)
	suite.assertLockedArchive("test-driver-1", "1.1.0", "test-driver-1.1.tar.gz")
	suite.driverIsNotInstalled("test-driver-1")
	suite.Require().Len(counters.downloadDirs, 1)
	suite.NoDirExists(counters.downloadDirs[0], "the download is removed")
}

func (suite *SubcommandTestSuite) TestLockSignatureVerification() {
	listPath := suite.writeDriverList("test-driver-1")
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(testBaseModel()))
	before := suite.readLockBytes()

	listPath = suite.writeDriverList("test-driver-1", "test-driver-no-sig")
	var counters syncCounters
	out := suite.runCmdErr(LockCmd{Path: listPath}.GetModelCustom(counters.counted(testBaseModel())))
	suite.Contains(out, "failed to verify signature")
	suite.Equal(before, suite.readLockBytes(), "an unverified archive is never pinned")
	for _, dir := range counters.downloadDirs {
		suite.NoDirExists(dir)
	}

	out = suite.runCmdErr(LockCmd{Path: listPath, Json: true}.GetModelCustom(testBaseModel()))
	suite.assertJSONErrorEnvelope(out, "lock_failed", "failed to verify signature")
	suite.Equal(before, suite.readLockBytes())
}

func (suite *SubcommandTestSuite) TestLockSignatureFailureWithoutLockWritesNothing() {
	listPath := suite.writeDriverList("test-driver-no-sig")
	suite.runCmdErr(LockCmd{Path: listPath}.GetModelCustom(testBaseModel()))
	suite.NoFileExists(suite.lockPath())
}

func (suite *SubcommandTestSuite) TestLockNoVerifyRecordsUnsignedArchive() {
	listPath := suite.writeDriverList("test-driver-no-sig")
	suite.runCmd(LockCmd{Path: listPath, NoVerify: true}.GetModelCustom(testBaseModel()))
	suite.assertLockedArchive("test-driver-no-sig", "1.0.0", "test-driver-no-sig.tar.gz")
	suite.driverIsNotInstalled("test-driver-no-sig")
}

func (suite *SubcommandTestSuite) TestLockUpgradesV1Lock() {
	listPath := suite.writeDriverList("test-driver-1")
	suite.writeV1Lock("1.0.0", config.PlatformTuple(), suite.libraryChecksum("test-driver-1.tar.gz"))

	var counters syncCounters
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(counters.counted(testBaseModel())))
	suite.Equal(1, counters.downloads, "the library proof needs the archive")
	suite.assertLockedArchive("test-driver-1", "1.0.0", "test-driver-1.tar.gz")
	suite.NoDirExists(counters.downloadDirs[0])
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestLockV1ChecksumMismatchKeepsLock() {
	listPath := suite.writeDriverList("test-driver-1")
	lock := suite.writeV1Lock("1.0.0", config.PlatformTuple(), strings.Repeat("0", 64))

	var counters syncCounters
	out := suite.runCmdErr(LockCmd{Path: listPath, Json: true}.GetModelCustom(counters.counted(testBaseModel())))
	suite.assertJSONErrorEnvelope(out, "checksum_failed", "checksum mismatch")
	suite.Equal(lock, suite.readLockBytes())
	suite.Require().Len(counters.downloadDirs, 1)
	suite.NoDirExists(counters.downloadDirs[0])
}

func (suite *SubcommandTestSuite) TestLockArchiveHashMismatchKeepsLock() {
	listPath := suite.writeDriverList("test-driver-1")
	// The v1 library proof forces a download, which the published hash rejects.
	lock := suite.writeV1Lock("1.0.0", config.PlatformTuple(), suite.libraryChecksum("test-driver-1.tar.gz"))

	out := suite.runCmdErr(LockCmd{Path: listPath, Json: true}.GetModelCustom(hashedBaseModel(strings.Repeat("0", 64))))
	suite.assertJSONErrorEnvelope(out, "checksum_failed", "archive SHA-256 mismatch")
	suite.Equal(lock, suite.readLockBytes())
}

func (suite *SubcommandTestSuite) TestLockChecksLockedArchiveSize() {
	f, err := openTestdataCopy("test-driver-1.tar.gz")
	suite.Require().NoError(err)
	defer removeDownload(f)
	info, err := f.Stat()
	suite.Require().NoError(err)
	target := testSyncTarget(suite.T(), config.PlatformTuple())

	wrong := info.Size() + 1
	pd := plannedDriver{Name: "test-driver-1", ArchiveSize: &wrong}
	var sizeErr *archiveSizeMismatchError
	suite.ErrorAs(checkDownloadedArchive(f, &pd, target, nil), &sizeErr)

	right := info.Size()
	pd = plannedDriver{Name: "test-driver-1", ArchiveSize: &right, Entry: lockedDriver{Artifacts: []lockedArtifact{{Target: target}}}}
	suite.Require().NoError(checkDownloadedArchive(f, &pd, target, nil))
	suite.Equal(lockHashPrefix+testArchiveSHA256(suite, "test-driver-1.tar.gz"), pd.Entry.Artifacts[0].Hash)
}

func (suite *SubcommandTestSuite) TestLockJSONOutput() {
	listPath := suite.writeDriverList("test-driver-1")
	hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
	out := suite.runCmd(LockCmd{Path: listPath, Json: true}.GetModelCustom(hashedBaseModel(hash)))

	var env jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(strings.TrimSpace(out)), &env), out)
	suite.Equal(jsonschema.SchemaVersion, env.SchemaVersion)
	suite.Equal("lock.status", env.Kind)
	var status jsonschema.LockStatus
	suite.Require().NoError(json.Unmarshal(env.Payload, &status))
	suite.Equal(jsonschema.LockStatus{
		LockFilePath: suite.lockPath(),
		Drivers:      []jsonschema.LockedDriver{{Name: "test-driver-1", Version: "1.0.0"}},
	}, status)
}

func (suite *SubcommandTestSuite) TestLockDownloadFailureWritesNoLock() {
	listPath := suite.writeDriverList("test-driver-1", "test-driver-2")
	base := testBaseModel()
	download := base.downloadPkg
	base.downloadPkg = func(pkg dbc.PkgInfo) (*os.File, error) {
		if pkg.Driver.Path == "test-driver-2" {
			return nil, fmt.Errorf("download refused")
		}
		return download(pkg)
	}
	var counters syncCounters
	out := suite.runCmdErr(LockCmd{Path: listPath}.GetModelCustom(counters.counted(base)))
	suite.Contains(out, "download refused")
	suite.NoFileExists(suite.lockPath(), "the lock file is written only after every driver succeeded")
	for _, dir := range counters.downloadDirs {
		suite.NoDirExists(dir)
	}
}

func (suite *SubcommandTestSuite) TestLockProjectLockWaitsForWorkerAfterQuit() {
	listPath := suite.writeDriverList("test-driver-1")
	base := testBaseModel()
	enteredDownload := make(chan struct{})
	continueDownload := make(chan struct{})
	var releaseDownload sync.Once
	allowDownloadReturn := func() { releaseDownload.Do(func() { close(continueDownload) }) }
	suite.T().Cleanup(allowDownloadReturn)
	base.downloadPkg = func(dbc.PkgInfo) (*os.File, error) {
		close(enteredDownload)
		<-continueDownload
		return nil, context.Canceled
	}

	model := LockCmd{Path: listPath}.newModel(base)
	suite.T().Cleanup(model.cancel)
	lockPath := filepath.Join(suite.tempdir, ".dbc.project.lock")
	finished := make(chan tea.Msg, 1)
	go func() { finished <- model.Init()() }()

	select {
	case <-enteredDownload:
	case <-time.After(projectLockWait):
		allowDownloadReturn()
		suite.FailNow("lock worker did not enter the blocked download")
	}
	assertProjectLockHeld(suite, lockPath)
	model.Update(quitKey)
	waitForProjectLockState(suite, model.projectLock, 1)
	assertProjectLockHeld(suite, lockPath)
	allowDownloadReturn()
	select {
	case msg := <-finished:
		suite.ErrorIs(msg.(error), context.Canceled)
	case <-time.After(projectLockWait):
		suite.FailNow("lock worker did not return after its download was released")
	}
	waitForProjectLockState(suite, model.projectLock, 0)
	acquireProjectLock(suite, lockPath)
	suite.NoFileExists(suite.lockPath())
}

func (suite *SubcommandTestSuite) TestLockReleasesProjectLockOnCompletion() {
	listPath := suite.writeDriverList("test-driver-1")
	suite.runCmd(LockCmd{Path: listPath}.GetModelCustom(hashedBaseModel(strings.Repeat("0", 64))))
	acquireProjectLock(suite, filepath.Join(suite.tempdir, ".dbc.project.lock"))
}

func (suite *SubcommandTestSuite) TestLockDriverListWithoutDrivers() {
	listPath := suite.writeDriverList()
	out := suite.runCmdErr(LockCmd{Path: listPath}.GetModelCustom(testBaseModel()))
	suite.Contains(out, "no drivers found")
	suite.NoFileExists(suite.lockPath())
}
