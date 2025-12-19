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
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.1.0 to "+suite.Dir()+"\n", out)
	suite.driverIsInstalled("test-driver-1", true)
}

func (suite *SubcommandTestSuite) TestInstallDriverNotFound() {
	m := InstallCmd{Driver: "foo", Level: suite.configLevel}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("Error: could not find driver: driver `foo` not found in driver registry index\r\n\r ", "", suite.runCmdErr(m))
	suite.driverIsNotInstalled("test-driver-1")
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
			m := InstallCmd{Driver: tt.driver, Level: suite.configLevel}.
				GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
			out := suite.runCmd(m)

			suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
				"\nInstalled test-driver-1 "+tt.expectedVersion+" to "+suite.Dir()+"\n", out)
			suite.driverIsInstalled("test-driver-1", true)
			m = UninstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.GetModelCustom(
				baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
			suite.runCmd(m)
		})
	}
}

func (suite *SubcommandTestSuite) TestInstallWithVersionLessSpace() {
	m := InstallCmd{Driver: "test-driver-1 < 1.1.0"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.0.0 to "+suite.tempdir+"\n", out)
}

func (suite *SubcommandTestSuite) TestReinstallUpdateVersion() {
	m := InstallCmd{Driver: "test-driver-1<=1.0.0"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.0.0 to "+suite.tempdir+"\n", suite.runCmd(m))

	m = InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nRemoved conflicting driver: test-driver-1 (version: 1.0.0)\nInstalled test-driver-1 1.1.0 to "+suite.tempdir+"\n",
		suite.runCmd(m))

	suite.Equal([]string{"test-driver-1.1/test-driver-1-not-valid.so",
		"test-driver-1.1/test-driver-1-not-valid.so.sig", "test-driver-1.toml"}, suite.getFilesInTempDir())
}

func (suite *SubcommandTestSuite) TestInstallVenv() {
	os.Unsetenv("ADBC_DRIVER_PATH")
	os.Setenv("VIRTUAL_ENV", suite.tempdir)
	defer os.Unsetenv("VIRTUAL_ENV")

	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
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
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	suite.FileExists(filepath.Join(driver_path, "test-driver-1.toml"))
	suite.NoFileExists(filepath.Join(venv_path, "test-driver-1.toml"))
	suite.NoFileExists(filepath.Join(conda_path, "test-driver-1.toml"))

	os.Unsetenv("ADBC_DRIVER_PATH")
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)
	suite.FileExists(filepath.Join(venv_path, "etc", "adbc", "drivers", "test-driver-1.toml"))
	suite.NoFileExists(filepath.Join(conda_path, "etc", "adbc", "drivers", "test-driver-1.toml"))

	os.Unsetenv("VIRTUAL_ENV")
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)
	suite.FileExists(filepath.Join(conda_path, "etc", "adbc", "drivers", "test-driver-1.toml"))

	os.Unsetenv("CONDA_PREFIX")
}

func (suite *SubcommandTestSuite) TestInstallCondaPrefix() {
	os.Unsetenv("ADBC_DRIVER_PATH")
	os.Setenv("CONDA_PREFIX", suite.tempdir)
	defer os.Unsetenv("CONDA_PREFIX")

	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.1.0 to "+filepath.Join(suite.tempdir, "etc", "adbc", "drivers")+"\n", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestInstallManifestOnlyDriver() {
	m := InstallCmd{Driver: "test-driver-manifest-only", Level: suite.configLevel}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})

	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-manifest-only 1.0.0 to "+suite.Dir()+"\n"+
			"\nMust have libtest_driver installed to load this driver\n", suite.runCmd(m))
	suite.driverIsInstalled("test-driver-manifest-only", false)
}

func (suite *SubcommandTestSuite) TestInstallDriverNoSignature() {
	m := InstallCmd{Driver: "test-driver-no-sig"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmdErr(m)
	suite.Contains(out, "signature file 'test-driver-1-not-valid.so.sig' for driver is missing")

	suite.Empty(suite.getFilesInTempDir())
	suite.NoDirExists(filepath.Join(suite.tempdir, "test-driver-no-sig"))

	m = InstallCmd{Driver: "test-driver-no-sig", NoVerify: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-no-sig 1.0.0 to "+suite.tempdir+"\n", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestInstallGitignoreDefaultBehavior() {
	driver_path := filepath.Join(suite.tempdir, "driver_path")
	ignorePath := filepath.Join(driver_path, ".gitignore")
	os.Setenv("ADBC_DRIVER_PATH", driver_path)
	defer os.Unsetenv("ADBC_DRIVER_PATH")

	suite.NoFileExists(ignorePath)

	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	_ = suite.runCmd(m)

	suite.FileExists(ignorePath)
}

func (suite *SubcommandTestSuite) TestInstallGitignoreExisingDir() {
	driver_path := filepath.Join(suite.tempdir, "driver_path")
	ignorePath := filepath.Join(driver_path, ".gitignore")
	os.Setenv("ADBC_DRIVER_PATH", driver_path)
	defer os.Unsetenv("ADBC_DRIVER_PATH")

	// Create the directory before we install the driver
	mkdirerr := os.MkdirAll(driver_path, 0o755)
	if mkdirerr != nil {
		suite.Error(mkdirerr)
	}

	suite.DirExists(driver_path)
	suite.NoFileExists(ignorePath)

	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	_ = suite.runCmd(m)

	// There shouldn't be a .gitignore because we didn't create the dir fresh
	// during install
	suite.NoFileExists(ignorePath)
}

func (suite *SubcommandTestSuite) TestInstallGitignorePreserveUserModified() {
	driver_path := filepath.Join(suite.tempdir, "driver_path")
	ignorePath := filepath.Join(driver_path, ".gitignore")
	os.Setenv("ADBC_DRIVER_PATH", driver_path)
	defer os.Unsetenv("ADBC_DRIVER_PATH")

	suite.NoFileExists(ignorePath)

	// First install - should create .gitignore
	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	_ = suite.runCmd(m)

	suite.FileExists(ignorePath)

	// User modifies the .gitignore file
	userContent := "# User's custom gitignore\n*.custom\n"
	err := os.WriteFile(ignorePath, []byte(userContent), 0o644)
	if err != nil {
		suite.Error(err)
	}

	// Second install - should preserve user's modifications
	m = UninstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	_ = suite.runCmd(m)
	m = InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	_ = suite.runCmd(m)

	// Verify the user's content is preserved
	data, err := os.ReadFile(ignorePath)
	if err != nil {
		suite.Error(err)
	}
	suite.Equal(userContent, string(data))
}

func (suite *SubcommandTestSuite) TestInstallCreatesSymlinks() {
	if runtime.GOOS == "windows" && (suite.configLevel == config.ConfigUser || suite.configLevel == config.ConfigSystem) {
		suite.T().Skip("Symlinks aren't created on Windows for User and System config levels")
	}

	// Install a driver
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	_ = suite.runCmd(m)
	suite.driverIsInstalled("test-driver-1", true)

	// Verify symlink is in place in the parent dir and is actually a symlink
	manifestPath := filepath.Join(suite.Dir(), "..", "test-driver-1.toml")
	suite.FileExists(manifestPath)
	info, err := os.Lstat(manifestPath)
	suite.NoError(err)
	suite.Equal(os.ModeSymlink, info.Mode()&os.ModeSymlink, "Expected test-driver-1.toml to be a symlink")
}
