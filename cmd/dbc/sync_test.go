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
)

func (suite *SubcommandTestSuite) TestSyncRejectsFutureLockFileWithoutRewriting() {
	listPath := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")
	lockContents := []byte("version = 3\nrevision = 0\n" +
		"[[drivers]]\nname = 'test-driver-1'\nversion = '1.0.0'\n" +
		"[drivers.source]\ntype = 'packslip'\nproject = 'github.com/example/test-driver-1'\n" +
		"[[drivers.artifacts]]\nformat = 'tar.gz'\n")
	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
	suite.Require().NoError(os.WriteFile(lockPath, lockContents, 0o644))

	model := SyncCmd{Path: listPath}.GetModelCustom(testBaseModel())
	output := suite.runCmdErr(model)
	suite.Contains(output, "unsupported lock file version 3")

	actual, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(lockContents, actual)
	suite.NoFileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
}
