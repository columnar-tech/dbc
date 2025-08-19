// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"path/filepath"
	"runtime"

	"github.com/columnar-tech/dbc/config"
)

func (suite *SubcommandTestSuite) TestInstall() {
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n"+
		"\r\nInstalled test-driver-1 1.1.0 to "+suite.tempdir+"\r\n", out)
	if runtime.GOOS != "windows" {
		suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
	}
}

func (suite *SubcommandTestSuite) TestInstallDriverNotFound() {
	m := InstallCmd{Driver: "foo", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("Error: could not find driver: driver `foo` not found in driver index\r\n\r ", suite.runCmdErr(m))
}
