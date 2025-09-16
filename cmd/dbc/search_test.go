// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"path/filepath"

	"github.com/columnar-tech/dbc/config"
)

func (suite *SubcommandTestSuite) TestSearchCmd() {
	m := SearchCmd{}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("• test-driver-1 - This is a test driver\r\n"+
		"• test-driver-2 - This is another test driver\r\n"+
		"• test-driver-manifest-only - This is manifest-only driver\r\n\r ", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSearchCmdWithInstalled() {
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	m = SearchCmd{}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("• test-driver-1 - This is a test driver [installed: env=>1.1.0]\r\n"+
		"• test-driver-2 - This is another test driver\r\n• test-driver-manifest-only - This is manifest-only driver\r\n\r ", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSearchCmdVerbose() {
	m := SearchCmd{Verbose: true}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("• test-driver-1\r\n   Title: Test Driver 1\r\n   "+
		"Description: This is a test driver\r\n   License: MIT\r\n   "+
		"Available Versions:\r\n    ├── 1.0.0\r\n    ╰── 1.1.0\r\n"+
		"• test-driver-2\r\n   Title: Test Driver 2\r\n   "+
		"Description: This is another test driver\r\n   License: Apache-2.0\r\n   "+
		"Available Versions:\r\n    ├── 2.0.0\r\n    ╰── 2.1.0\r\n"+
		"• test-driver-manifest-only\r\n   Title: Test Driver Manifest Only\r\n   "+
		"Description: This is manifest-only driver\r\n   License: Apache-2.0\r\n   "+
		"Available Versions:\r\n    ╰── 1.0.0\r\n\r ", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSearchCmdVerboseWithInstalled() {
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	m = SearchCmd{Verbose: true}.GetModelCustom(
		baseModel{getDriverList: getTestDriverList,
			downloadPkg: downloadTestPkg})
	suite.validateOutput("• test-driver-1 [installed: env=>1.1.0]\r\n   Title: Test Driver 1\r\n   "+
		"Description: This is a test driver\r\n   License: MIT\r\n   "+
		"Installed Versions:\r\n    ╰── 1.1.0\r\n        ╰── env => "+filepath.Join(suite.tempdir)+
		"\r\n   Available Versions:\r\n    ├── 1.0.0\r\n    ╰── 1.1.0\r\n"+
		"• test-driver-2\r\n   Title: Test Driver 2\r\n   "+
		"Description: This is another test driver\r\n   License: Apache-2.0\r\n   "+
		"Available Versions:\r\n    ├── 2.0.0\r\n    ╰── 2.1.0\r\n"+
		"• test-driver-manifest-only\r\n"+
		"   Title: Test Driver Manifest Only\r\n"+
		"   Description: This is manifest-only driver\r\n"+
		"   License: Apache-2.0\r\n"+
		"   Available Versions:\r\n"+
		"    ╰── 1.0.0\r\n\r ", suite.runCmd(m))
}
