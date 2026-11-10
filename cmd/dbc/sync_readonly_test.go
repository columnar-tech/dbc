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
	"runtime"

	"github.com/columnar-tech/dbc/config"
)

func (suite *SubcommandTestSuite) TestSyncReusesInstalledPackageWithReadOnlyInstallRoot() {
	if runtime.GOOS == "windows" || runtime.GOOS == "js" || os.Geteuid() == 0 {
		suite.T().Skip("read-only mode-bit checks require a non-elevated native Unix user")
	}
	if suite.configLevel != config.ConfigEnv {
		suite.T().Skip("test uses an isolated environment-level install root")
	}

	projectDir := suite.T().TempDir()
	listPath := filepath.Join(projectDir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(listPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(testBaseModel()))

	root := suite.tempdir
	lockPaths := []string{
		filepath.Join(root, ".test-driver-1.dbc"),
		filepath.Join(root, ".dbc.namespace.lock"),
	}
	for _, path := range lockPaths {
		if _, err := os.Stat(path); err != nil {
			suite.T().Skipf("expected install lock file %s: %v", path, err)
		}
	}
	rootInfo, err := os.Stat(root)
	suite.Require().NoError(err)
	rootMode := rootInfo.Mode().Perm()
	suite.T().Cleanup(func() { _ = os.Chmod(root, rootMode) })
	for _, path := range lockPaths {
		info, statErr := os.Stat(path)
		suite.Require().NoError(statErr)
		mode := info.Mode().Perm()
		lockPath, lockMode := path, mode
		suite.T().Cleanup(func() { _ = os.Chmod(lockPath, lockMode) })
		suite.Require().NoError(os.Chmod(path, 0o444))
		file, openErr := os.OpenFile(path, os.O_RDWR, 0o600)
		if openErr == nil {
			_ = file.Close()
			suite.T().Skip("filesystem does not enforce read-only file permissions")
		}
	}
	if err := os.Chmod(root, 0o555); err != nil {
		suite.T().Skipf("cannot make install root read-only: %v", err)
	}
	if file, err := os.CreateTemp(root, ".permission-probe-"); err == nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		suite.T().Skip("filesystem does not enforce read-only directory permissions")
	}

	var counters syncCounters
	base := counters.counted(testBaseModel())
	suite.runCmd(SyncCmd{Path: listPath, Level: suite.configLevel}.GetModelCustom(base))
	suite.Zero(counters.index, "the locked sync should not fetch the merged registry index")
	suite.Zero(counters.boundIndex, "the locked sync should not fetch a registry index")
	suite.Zero(counters.downloads, "the installed archive should be reused")
	suite.driverIsInstalledWithVersion("test-driver-1", "1.1.0", true)
	suite.FileExists(filepath.Join(projectDir, "dbc.lock"))
}
