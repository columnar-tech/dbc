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
	"os"
	"path/filepath"

	"github.com/columnar-tech/dbc/config"
)

func (suite *SubcommandTestSuite) TestSyncChecksLockedArchiveSize() {
	for _, tc := range []struct {
		name   string
		delta  int64
		stream bool
	}{
		{name: "matching size"},
		{name: "wrong size", delta: 1},
		{name: "wrong size stream", delta: 1, stream: true},
	} {
		suite.Run(tc.name, func() {
			// Keep an installation of a different version so reuse cannot skip
			// the download and its optional size check.
			listPath := suite.writeDriverList("test-driver-1")
			hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
			suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(hashedBaseModel(hash)))
			suite.runCmd(InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.GetModelCustom(testBaseModel()))
			old := suite.getInstalledDriver("test-driver-1")
			suite.Require().Equal("1.1.0", old.Version.String())
			oldLibrary := old.Driver.Shared.Get(config.PlatformTuple())
			oldBytes, err := os.ReadFile(oldLibrary)
			suite.Require().NoError(err)
			receiptPath := suite.receiptPath("test-driver-1")
			oldReceipt, err := os.ReadFile(receiptPath)
			suite.Require().NoError(err)
			oldLock := suite.setTestLockedArchiveSize(tc.delta)

			var counters syncCounters
			model := SyncCmd{Path: listPath, Level: suite.configLevel, NoVerify: tc.delta != 0, Json: tc.delta != 0, JsonStreamProgress: tc.stream}.
				GetModelCustom(counters.counted(unreachableRegistries(hashedBaseModel(hash))))
			if tc.delta == 0 {
				suite.runCmd(model)
				suite.assertReceipt("test-driver-1", "test-driver-1.tar.gz", true)
			} else {
				out := suite.runCmdErr(model)
				suite.assertJSONErrorEnvelope(out, "checksum_failed", "archive size mismatch")
				current := suite.getInstalledDriver("test-driver-1")
				suite.Equal(old.Driver.Shared.Get(config.PlatformTuple()), current.Driver.Shared.Get(config.PlatformTuple()))
				currentReceipt, err := os.ReadFile(receiptPath)
				suite.Require().NoError(err)
				suite.Equal(oldReceipt, currentReceipt)
				currentLock, err := os.ReadFile(suite.lockPath())
				suite.Require().NoError(err)
				suite.Equal(oldLock, currentLock)
				currentBytes, err := os.ReadFile(oldLibrary)
				suite.Require().NoError(err)
				suite.Equal(oldBytes, currentBytes)
			}
			suite.Equal(1, counters.downloads)
			for _, dir := range counters.downloadDirs {
				suite.NoDirExists(dir)
			}
			suite.Require().NoError(os.Remove(suite.lockPath()))
		})
	}
}

func (suite *SubcommandTestSuite) TestSyncReusesSizedLockedArchive() {
	if !suite.supportsReuse() {
		suite.T().Skip("installed package reuse requires pinned directory handles")
	}
	for _, delta := range []int64{0, 1} {
		listPath := suite.writeDriverList("test-driver-1")
		hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
		suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(hashedBaseModel(hash)))
		old := suite.getInstalledDriver("test-driver-1")
		receiptPath := suite.receiptPath("test-driver-1")
		oldReceipt, err := os.ReadFile(receiptPath)
		suite.Require().NoError(err)
		oldLock := suite.setTestLockedArchiveSize(delta)

		// Even an inconsistent size is checked only if an archive needs to be
		// downloaded. Matching archive hashes and signatures suffice for reuse.
		var counters syncCounters
		suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.
			GetModelCustom(counters.counted(unreachableRegistries(hashedBaseModel(hash)))))
		suite.Zero(counters.downloads)
		suite.Zero(counters.index)
		suite.Zero(counters.boundIndex)
		current := suite.getInstalledDriver("test-driver-1")
		suite.Equal(old.Driver.Shared.Get(config.PlatformTuple()), current.Driver.Shared.Get(config.PlatformTuple()))
		currentReceipt, err := os.ReadFile(receiptPath)
		suite.Require().NoError(err)
		suite.Equal(oldReceipt, currentReceipt)
		currentLock, err := os.ReadFile(suite.lockPath())
		suite.Require().NoError(err)
		suite.Equal(oldLock, currentLock)
		suite.Require().NoError(os.Remove(suite.lockPath()))
	}
}

// setTestLockedArchiveSize adds size metadata to the single-artifact fixture.
func (suite *SubcommandTestSuite) setTestLockedArchiveSize(delta int64) []byte {
	suite.T().Helper()
	lf, err := readLockFile(suite.lockPath())
	suite.Require().NoError(err)
	suite.Require().Len(lf.V2.Drivers, 1)
	suite.Require().Len(lf.V2.Drivers[0].Artifacts, 1)
	info, err := os.Stat(filepath.Join("testdata", "test-driver-1.tar.gz"))
	suite.Require().NoError(err)
	size := info.Size() + delta
	lf.V2.Drivers[0].Artifacts[0].Size = &size
	suite.Require().NoError(writeLockFileV2(suite.lockPath(), lf.V2))
	data, err := os.ReadFile(suite.lockPath())
	suite.Require().NoError(err)
	return data
}
