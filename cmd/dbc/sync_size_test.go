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
		name  string
		delta int64
	}{
		{name: "matching size"},
		{name: "wrong size", delta: 1},
	} {
		suite.Run(tc.name, func() {
			// Each case starts with a valid signed installation. A receipt has
			// no archive-size evidence, so even this installation must download
			// again when the lock supplies a size.
			listPath := suite.writeDriverList("test-driver-1")
			hash := testArchiveSHA256(suite, "test-driver-1.tar.gz")
			suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(hashedBaseModel(hash)))
			old := suite.getInstalledDriver("test-driver-1")
			receiptPath := suite.receiptPath("test-driver-1")
			oldReceipt, err := os.ReadFile(receiptPath)
			suite.Require().NoError(err)
			lf, err := readLockFile(suite.lockPath())
			suite.Require().NoError(err)
			info, err := os.Stat(filepath.Join("testdata", "test-driver-1.tar.gz"))
			suite.Require().NoError(err)
			size := info.Size() + tc.delta
			for i := range lf.V2.Drivers[0].Artifacts {
				lf.V2.Drivers[0].Artifacts[i].Size = &size
			}
			suite.Require().NoError(writeLockFileV2(suite.lockPath(), lf.V2))
			oldLock, err := os.ReadFile(suite.lockPath())
			suite.Require().NoError(err)

			var counters syncCounters
			model := SyncCmd{Path: listPath, Level: suite.configLevel, NoVerify: tc.delta != 0, Json: tc.delta != 0}.
				GetModelCustom(counters.counted(unreachableRegistries(hashedBaseModel(hash))))
			if tc.delta == 0 {
				suite.runCmd(model)
				suite.assertReceipt("test-driver-1", "test-driver-1.tar.gz", true)
			} else {
				out := suite.runCmdErr(model)
				suite.assertJSONErrorEnvelope(out, "sync_failed", "archive size mismatch")
				current := suite.getInstalledDriver("test-driver-1")
				suite.Equal(old.Driver.Shared.Get(config.PlatformTuple()), current.Driver.Shared.Get(config.PlatformTuple()))
				currentReceipt, err := os.ReadFile(receiptPath)
				suite.Require().NoError(err)
				suite.Equal(oldReceipt, currentReceipt)
				currentLock, err := os.ReadFile(suite.lockPath())
				suite.Require().NoError(err)
				suite.Equal(oldLock, currentLock)
			}
			suite.Equal(1, counters.downloads)
			for _, dir := range counters.downloadDirs {
				suite.NoDirExists(dir)
			}
			suite.Require().NoError(os.Remove(suite.lockPath()))
		})
	}
}
