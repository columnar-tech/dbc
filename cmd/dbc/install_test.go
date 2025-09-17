// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/columnar-tech/dbc/config"
)

func (suite *SubcommandTestSuite) TestInstall() {
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.1.0 to "+suite.tempdir+"\n", out)
	if runtime.GOOS != "windows" {
		suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
	}
}

func (suite *SubcommandTestSuite) TestInstallDriverNotFound() {
	m := InstallCmd{Driver: "foo", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("Error: could not find driver: driver `foo` not found in driver index\r\n\r ", "", suite.runCmdErr(m))
}

func (suite *SubcommandTestSuite) TestInstallWithVersion() {
	m := InstallCmd{Driver: "test-driver-1<=1.0.0"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.0.0 to "+suite.tempdir+"\n", out)
}

func (suite *SubcommandTestSuite) TestInstallWithVersionLessSpace() {
	m := InstallCmd{Driver: "test-driver-1 < 1.1.0"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.0.0 to "+suite.tempdir+"\n", out)
}

func (suite *SubcommandTestSuite) TestInstallUserFake() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	os.Unsetenv("ADBC_DRIVER_PATH")

	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	installModel := m.(progressiveInstallModel)
	suite.Equal(installModel.cfg.Level, config.ConfigUser)
	installModel.cfg.Location = filepath.Join(suite.tempdir, "root", installModel.cfg.Location)
	m = installModel // <- We need to reassign to make the change stick
	suite.runCmd(m)
	suite.FileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))
}

func (suite *SubcommandTestSuite) TestInstallUserFakeExplicit() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	os.Unsetenv("ADBC_DRIVER_PATH")

	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigUser}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	installModel := m.(progressiveInstallModel)
	suite.Equal(installModel.cfg.Level, config.ConfigUser)
	installModel.cfg.Location = filepath.Join(suite.tempdir, "root", installModel.cfg.Location)
	m = installModel // <- We need to reassign to make the change stick
	suite.runCmd(m)
	suite.FileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))
}

func (suite *SubcommandTestSuite) TestInstallSystemFake() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigSystem}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	installModel := m.(progressiveInstallModel)
	suite.Equal(installModel.cfg.Level, config.ConfigSystem)
	installModel.cfg.Location = filepath.Join(suite.tempdir, "root", installModel.cfg.Location)
	m = installModel // <- We need to reassign to make the change stick
	suite.runCmd(m)
	suite.FileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))
}

func (suite *SubcommandTestSuite) TestInstallVenv() {
	os.Unsetenv("ADBC_DRIVER_PATH")
	os.Setenv("VIRTUAL_ENV", suite.tempdir)
	defer os.Unsetenv("VIRTUAL_ENV")

	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.1.0 to "+filepath.Join(suite.tempdir, "etc", "adbc", "drivers")+"\n", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestInstallCondaPrefix() {
	os.Unsetenv("ADBC_DRIVER_PATH")
	os.Setenv("CONDA_PREFIX", suite.tempdir)
	defer os.Unsetenv("CONDA_PREFIX")

	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.1.0 to "+filepath.Join(suite.tempdir, "etc", "adbc", "drivers")+"\n", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestInstallUserFakeExplicitLevelOverrides() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	// If the user explicitly sets level, it should override ADBC_DRIVER_PATH
	// which, when testing, is set to tempdir
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigSystem}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	installModel := m.(progressiveInstallModel)
	suite.Equal(installModel.cfg.Level, config.ConfigSystem)
	installModel.cfg.Location = filepath.Join(suite.tempdir, "user", installModel.cfg.Location)
	m = installModel // <- We need to reassign to make the change stick
	suite.runCmd(m)
	suite.FileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))
}

func (suite *SubcommandTestSuite) TestInstallManifestOnlyDriver() {
	m := InstallCmd{Driver: "test-driver-manifest-only"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-manifest-only 1.0.0 to "+suite.tempdir+"\n"+
			"\nMust have libtest_driver installed to load this driver\n", suite.runCmd(m))
	if runtime.GOOS != "windows" {
		suite.FileExists(filepath.Join(suite.tempdir, "test-driver-manifest-only.toml"))
	}
}

func (suite *SubcommandTestSuite) TestInstallDriverNoSignature() {
	m := InstallCmd{Driver: "test-driver-no-sig"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	out := suite.runCmdErr(m)
	suite.Contains(out, "signature file 'test-driver-1-not-valid.so.sig' for driver is missing")

	filelist := []string{}
	suite.NoError(fs.WalkDir(os.DirFS(suite.tempdir), ".", func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			return nil
		}

		filelist = append(filelist, path)
		return nil
	}))

	// currently we don't clean out the downloaded file if signature verification fails
	suite.Equal([]string{"test-driver-no-sig/test-driver-1-not-valid.so"}, filelist)

	m = InstallCmd{Driver: "test-driver-no-sig", NoVerify: true}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-no-sig 1.0.0 to "+suite.tempdir+"\n", suite.runCmd(m))
}
