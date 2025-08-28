// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"os"
	"path"
	"path/filepath"
	"runtime"

	"github.com/columnar-tech/dbc/config"
)

func (suite *SubcommandTestSuite) TestUninstallNotFound() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	m := UninstallCmd{Driver: "notfound"}.GetModel()
	suite.validateOutput("Error: failed to find driver `notfound` in order to uninstall it: error opening manifest "+
		filepath.Join(suite.tempdir, "notfound.toml")+": open "+filepath.Join(suite.tempdir, "notfound.toml")+": no such file or directory\r\n\r ", suite.runCmdErr(m))
}

func (suite *SubcommandTestSuite) TestUninstallManifestOnly() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	contents := `name = "Some Found Driver"

	# Doesn't matter what's in here

	[Driver]
	entrypoint = "some_entry"
	shared = "some.dll"`
	os.WriteFile(path.Join(suite.tempdir, "found.toml"), []byte(contents), 0644)

	m := UninstallCmd{Driver: "found", Level: config.ConfigEnv}.GetModel()
	suite.validateOutput("Driver `found` uninstalled successfully!\r\n\r\n\r ", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestUninstallDriverAndManifest() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	pkgdir := path.Join(suite.tempdir, "somepath")
	os.Mkdir(pkgdir, 0o755)
	contents := `name = "Found Driver"

	# Doesn't matter what's in here

	[Driver]
	[Driver.shared]
	"some_platform" = "` + pkgdir + `/some.dll"`
	os.WriteFile(path.Join(suite.tempdir, "found.toml"), []byte(contents), 0o644)
	os.WriteFile(path.Join(pkgdir, "some.dll"), []byte("anything"), 0o644)

	m := UninstallCmd{Driver: "found", Level: config.ConfigEnv}.GetModel()
	suite.validateOutput("Driver `found` uninstalled successfully!\r\n\r\n\r ", suite.runCmd(m))
}

// Test what happens when a user installs a driver in multiple locations
// and doesn't specify which level to uninstall from
func (suite *SubcommandTestSuite) TestUninstallMultipleLocations() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	// Install to Env first
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.runCmd(m)
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	// Then System (here, we fake it as $tempdir/etc/adbc)
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigSystem}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	installModel := m.(progressiveInstallModel)
	installModel.cfg.Location = filepath.Join(suite.tempdir, "root", installModel.cfg.Location)
	m = installModel // <- We need to reassign to make the change stick
	suite.runCmd(m)
	suite.FileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))

	// Uninstall from Env level
	m = UninstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	suite.NoFileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
	suite.FileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))
}

// Test whether the use can override the default behavior and uninstall
// a driver at a specific level
func (suite *SubcommandTestSuite) TestUninstallMultipleLocationsNonDefault() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	// Install to Env first
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.runCmd(m)
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	// Then System (here, we fake it as $tempdir/etc/adbc)
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigSystem}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	installModel := m.(progressiveInstallModel)
	installModel.cfg.Location = filepath.Join(suite.tempdir, "root", installModel.cfg.Location)
	m = installModel // <- We need to reassign to make the change stick
	suite.runCmd(m)
	suite.FileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))

	// Then uninstall System (again, faked as $tempdir/etc/adbc)
	m = UninstallCmd{Driver: "test-driver-1", Level: config.ConfigSystem}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	uninstallModel := m.(uninstallModel)
	uninstallModel.cfg.Location = filepath.Join(suite.tempdir, "root", uninstallModel.cfg.Location)
	m = uninstallModel // <- We need to reassign to make the change stick
	suite.runCmd(m)

	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
	suite.NoFileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))
}
