// Copyright 2025 Columnar Technologies Inc.
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
	tests := []struct {
		driver          string
		expectedVersion string
	}{
		{"test-driver-1=1.0.0", "1.0.0"},
		{"test-driver-1<=1.0.0", "1.0.0"},
		{"test-driver-1<1.1.0", "1.0.0"},
		{"test-driver-1~1.0", "1.0.0"},
		{"test-driver-1^1.0", "1.1.0"},
	}

	for _, tt := range tests {
		suite.Run(tt.driver, func() {
			m := InstallCmd{Driver: tt.driver}.
				GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
			out := suite.runCmd(m)
			suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
				"\nInstalled test-driver-1 "+tt.expectedVersion+" to "+suite.tempdir+"\n", out)

			m = UninstallCmd{Driver: "test-driver-1"}.GetModelCustom(
				baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
			suite.runCmd(m)
		})
	}
}

func (suite *SubcommandTestSuite) TestInstallWithVersionLessSpace() {
	m := InstallCmd{Driver: "test-driver-1 < 1.1.0"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.0.0 to "+suite.tempdir+"\n", out)
}

func (suite *SubcommandTestSuite) TestReinstallUpdateVersion() {
	m := InstallCmd{Driver: "test-driver-1<=1.0.0"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.0.0 to "+suite.tempdir+"\n", suite.runCmd(m))

	m = InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nRemoved conflicting driver: test-driver-1 (version: 1.0.0)\nInstalled test-driver-1 1.1.0 to "+suite.tempdir+"\n",
		suite.runCmd(m))

	suite.Equal([]string{"test-driver-1.1/test-driver-1-not-valid.so",
		"test-driver-1.1/test-driver-1-not-valid.so.sig", "test-driver-1.toml"}, suite.getFilesInTempDir())
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

func (suite *SubcommandTestSuite) TestInstallEnvironmentPrecedence() {
	// Like the driver managers, dbc follows a precedence chain when
	// ADBC_DRIVER_MANAGER, VIRTUAL_ENV, and CONDA_PREFIX are set with each
	// variable overriding the next.
	driver_path := filepath.Join(suite.tempdir, "driver_path")
	venv_path := filepath.Join(suite.tempdir, "venv_path")
	conda_path := filepath.Join(suite.tempdir, "conda_path")
	os.Setenv("ADBC_DRIVER_PATH", driver_path)
	os.Setenv("VIRTUAL_ENV", venv_path)
	os.Setenv("CONDA_PREFIX", conda_path)

	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	suite.FileExists(filepath.Join(driver_path, "test-driver-1.toml"))
	suite.NoFileExists(filepath.Join(venv_path, "test-driver-1.toml"))
	suite.NoFileExists(filepath.Join(conda_path, "test-driver-1.toml"))

	os.Unsetenv("ADBC_DRIVER_PATH")
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.runCmd(m)
	suite.FileExists(filepath.Join(venv_path, "etc", "adbc", "drivers", "test-driver-1.toml"))
	suite.NoFileExists(filepath.Join(conda_path, "etc", "adbc", "drivers", "test-driver-1.toml"))

	os.Unsetenv("VIRTUAL_ENV")
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.runCmd(m)
	suite.FileExists(filepath.Join(conda_path, "etc", "adbc", "drivers", "test-driver-1.toml"))

	os.Unsetenv("CONDA_PREFIX")
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

	suite.Empty(suite.getFilesInTempDir())
	suite.NoDirExists(filepath.Join(suite.tempdir, "test-driver-no-sig"))

	m = InstallCmd{Driver: "test-driver-no-sig", NoVerify: true}.
		GetModelCustom(baseModel{getDriverList: getTestDriverList, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-no-sig 1.0.0 to "+suite.tempdir+"\n", suite.runCmd(m))
}
