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
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"

	"github.com/columnar-tech/dbc/config"
	"github.com/pelletier/go-toml/v2"
)

func (suite *SubcommandTestSuite) TestUninstallNotFound() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	m := UninstallCmd{Driver: "notfound"}.GetModel()
	suite.validateOutput("Error: failed to find driver `notfound` in order to uninstall it: searched "+suite.tempdir+
		"\r\n\r ", "", suite.runCmdErr(m))
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
	suite.validateOutput("Driver `found` uninstalled successfully!\r\n\r\n\r ", "", suite.runCmd(m))
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
	suite.validateOutput("Driver `found` uninstalled successfully!\r\n\r\n\r ", "", suite.runCmd(m))
}

// Test what happens when a user installs a driver in multiple locations
// and doesn't specify which level to uninstall from
func (suite *SubcommandTestSuite) TestUninstallMultipleLocations() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	// Install to Env first
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	// Then System (here, we fake it as $tempdir/etc/adbc)
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigSystem}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	installModel := m.(progressiveInstallModel)
	installModel.cfg.Location = filepath.Join(suite.tempdir, "root", installModel.cfg.Location)
	m = installModel // <- We need to reassign to make the change stick
	suite.runCmd(m)
	suite.FileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))

	// Uninstall from Env level
	m = UninstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
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
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	// Then System (here, we fake it as $tempdir/etc/adbc)
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigSystem}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	installModel := m.(progressiveInstallModel)
	installModel.cfg.Location = filepath.Join(suite.tempdir, "root", installModel.cfg.Location)
	m = installModel // <- We need to reassign to make the change stick
	suite.runCmd(m)
	suite.FileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))

	// Then uninstall System (again, faked as $tempdir/etc/adbc)
	m = UninstallCmd{Driver: "test-driver-1", Level: config.ConfigSystem}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	uninstallModel := m.(uninstallModel)
	uninstallModel.cfg.Location = filepath.Join(suite.tempdir, "root", uninstallModel.cfg.Location)
	m = uninstallModel // <- We need to reassign to make the change stick
	suite.runCmd(m)

	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
	suite.NoFileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))
}

func (suite *SubcommandTestSuite) TestUninstallManifestOnlyDriver() {
	m := InstallCmd{Driver: "test-driver-manifest-only"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-manifest-only 1.0.0 to "+suite.tempdir+"\n"+
			"\nMust have libtest_driver installed to load this driver\n", suite.runCmd(m))
	if runtime.GOOS != "windows" {
		suite.FileExists(filepath.Join(suite.tempdir, "test-driver-manifest-only.toml"))
	}

	// Verify the sidecar folder exists before we uninstall
	new_sidecar_path := fmt.Sprintf("test-driver-manifest-only_%s_v1.0.0", config.PlatformTuple())
	err := os.Rename(filepath.Join(suite.tempdir, "test-driver-manifest-only"), filepath.Join(suite.tempdir, new_sidecar_path))
	if err != nil {
		suite.Fail(fmt.Sprintf("Failed to rename sidecar folder. Something is wrong with this test: %v", err))
	}
	suite.DirExists(filepath.Join(suite.tempdir, new_sidecar_path))

	// Now uninstall and verify we clean up
	m = UninstallCmd{Driver: "test-driver-manifest-only"}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.validateOutput("Driver `test-driver-manifest-only` uninstalled successfully!\r\n\r\n\r ", "", suite.runCmd(m))
	if runtime.GOOS != "windows" {
		suite.NoFileExists(filepath.Join(suite.tempdir, "test-driver-manifest-only.toml"))
	}
	suite.NoDirExists(filepath.Join(suite.tempdir, new_sidecar_path))
}

// See https://github.com/columnar-tech/dbc/issues/37
func (suite *SubcommandTestSuite) TestUninstallInvalidManifest() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}
	m := InstallCmd{Driver: "test-driver-invalid-manifest", Level: config.ConfigEnv}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-invalid-manifest.toml"))

	// The installed manifest should have a Driver.shared set to a folder, not the .so
	// We only need a partial struct definition to read in the Driver.shared table
	type partialManifest struct {
		Driver struct {
			Shared map[string]string `toml:"shared"`
		}
	}
	var invalidManifest partialManifest
	f, err := os.Open(filepath.Join(suite.tempdir, "test-driver-invalid-manifest.toml"))
	if err != nil {
		suite.Error(err)
	}
	err = toml.NewDecoder(f).Decode(&invalidManifest)
	if err != nil {
		suite.Error(err)
	}
	value := invalidManifest.Driver.Shared[config.PlatformTuple()]
	// Assert that it's a folder
	suite.DirExists(value)
	// and continue

	m = UninstallCmd{Driver: "test-driver-invalid-manifest", Level: config.ConfigEnv}.GetModel()
	output := suite.runCmd(m)

	suite.validateOutput("Driver `test-driver-invalid-manifest` uninstalled successfully!\r\n\r\n\r ", "", output)

	// Ensure we don't nuke the tempdir which is the original (major) issue
	suite.DirExists(suite.tempdir)

	// We do remove the manifest
	suite.NoFileExists(filepath.Join(suite.tempdir, "test-driver-invalid-manifest.toml"))
	// But we don't remove the driver shared folder in this edge case, so we assert
	// they're still around
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-invalid-manifest", "libadbc_driver_invalid_manifest.so"))
}
