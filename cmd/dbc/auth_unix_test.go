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

//go:build !windows

package main

import (
	"os"
	"path/filepath"

	"github.com/columnar-tech/dbc/auth"
)

func (suite *SubcommandTestSuite) TestLicenseInstallUnreadableSource() {
	tmpDir := suite.T().TempDir()
	credPath := filepath.Join(tmpDir, "credentials.toml")
	restore := auth.SetCredPathForTesting(credPath)
	defer restore()

	// Create source file with no read permissions
	srcFile := filepath.Join(suite.T().TempDir(), "columnar.lic")
	suite.Require().NoError(os.WriteFile(srcFile, []byte("license-data"), 0o000))

	cmd := LicenseInstallCmd{LicensePath: srcFile}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	out := suite.runCmdErr(m)
	suite.Contains(out, "failed to read license file")
}

func (suite *SubcommandTestSuite) TestLicenseInstallUnwritableTarget() {
	tmpDir := suite.T().TempDir()
	// Point credPath into an unwritable directory
	unwritableDir := filepath.Join(tmpDir, "readonly")
	suite.Require().NoError(os.MkdirAll(unwritableDir, 0o500))
	credPath := filepath.Join(unwritableDir, "nested", "credentials.toml")
	restore := auth.SetCredPathForTesting(credPath)
	defer restore()

	srcFile := filepath.Join(suite.T().TempDir(), "columnar.lic")
	suite.Require().NoError(os.WriteFile(srcFile, []byte("license-data"), 0o600))

	cmd := LicenseInstallCmd{LicensePath: srcFile, Force: true}
	m := cmd.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg:       downloadTestPkg,
	})

	out := suite.runCmdErr(m)
	suite.Contains(out, "failed to create credentials directory")
}
