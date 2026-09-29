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
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

func (suite *SubcommandTestSuite) TestUninstallNotFound() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	m := UninstallCmd{Driver: "notfound"}.GetModel()
	suite.validateOutput("\r ", "\nError: failed to find driver `notfound` in order to uninstall it: searched "+suite.tempdir, suite.runCmdErr(m))
}

func (suite *SubcommandTestSuite) TestUninstallManifestOnly() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	contents := `name = "Some Found Driver"
version = "1.0.0"

	# Doesn't matter what's in here

	[Driver]
	entrypoint = "some_entry"
	shared = "some.dll"`
	os.WriteFile(path.Join(suite.tempdir, "found.toml"), []byte(contents), 0644)

	m := UninstallCmd{Driver: "found", Level: config.ConfigEnv}.GetModel()
	suite.validateOutput("\r ", "Driver `found` uninstalled successfully!", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestUninstallDriverAndManifest() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	pkgdir := path.Join(suite.tempdir, "somepath")
	os.Mkdir(pkgdir, 0o755)
	contents := `name = "Found Driver"
version = "1.0.0"

	# Doesn't matter what's in here

	[Driver]
	[Driver.shared]
	"some_platform" = "` + pkgdir + `/some.dll"`
	os.WriteFile(path.Join(suite.tempdir, "found.toml"), []byte(contents), 0o644)
	os.WriteFile(path.Join(pkgdir, "some.dll"), []byte("anything"), 0o644)

	m := UninstallCmd{Driver: "found", Level: config.ConfigEnv}.GetModel()
	suite.validateOutput("\r ", "Driver `found` uninstalled successfully!", suite.runCmd(m))
}

// Test what happens when a user installs a driver in multiple locations
// and doesn't specify which level to uninstall from
func (suite *SubcommandTestSuite) TestUninstallMultipleLocations() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	// Install to Env first
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	suite.runCmd(m)
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	// Then System (here, we fake it as $tempdir/etc/adbc)
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigSystem}.
		GetModelCustom(testBaseModel())
	installModel := m.(progressiveInstallModel)
	installModel.cfg.Location = filepath.Join(suite.tempdir, "root", installModel.cfg.Location)
	m = installModel // <- We need to reassign to make the change stick
	suite.runCmd(m)
	suite.FileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))

	// Uninstall from Env level
	m = UninstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	suite.runCmd(m)

	suite.NoFileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
	suite.FileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))
}

func (suite *SubcommandTestSuite) TestUninstallDriverTwice() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	// Install to Env first
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	suite.runCmd(m)
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	// Uninstall from Env level
	m = UninstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	suite.runCmd(m)

	suite.NoFileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	// Uninstall from Env level
	m = UninstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r ", "\nError: failed to find driver `test-driver-1` in order to uninstall it: searched "+suite.tempdir, suite.runCmdErr(m))
}

// Test whether the use can override the default behavior and uninstall
// a driver at a specific level
func (suite *SubcommandTestSuite) TestUninstallMultipleLocationsNonDefault() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	// Install to Env first
	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	suite.runCmd(m)
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	// Then System (here, we fake it as $tempdir/etc/adbc)
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigSystem}.
		GetModelCustom(testBaseModel())
	installModel := m.(progressiveInstallModel)
	installModel.cfg.Location = filepath.Join(suite.tempdir, "root", installModel.cfg.Location)
	m = installModel // <- We need to reassign to make the change stick
	suite.runCmd(m)
	suite.FileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))

	// Then uninstall System (again, faked as $tempdir/etc/adbc)
	m = UninstallCmd{Driver: "test-driver-1", Level: config.ConfigSystem}.
		GetModelCustom(testBaseModel())
	uninstallModel := m.(uninstallModel)
	uninstallModel.cfg.Location = filepath.Join(suite.tempdir, "root", uninstallModel.cfg.Location)
	m = uninstallModel // <- We need to reassign to make the change stick
	suite.runCmd(m)

	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
	suite.NoFileExists(filepath.Join(installModel.cfg.Location, "test-driver-1.toml"))
}

func (suite *SubcommandTestSuite) TestUninstallManifestOnlyDriver() {
	m := InstallCmd{Driver: "test-driver-manifest-only", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())

	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-manifest-only 1.0.0 to "+suite.Dir()+
			"\n\nMust have libtest_driver installed to load this driver", suite.runCmd(m))
	suite.driverIsInstalled("test-driver-manifest-only", false)

	// The transactional installer stores package-owned metadata in a receipt-backed generation.
	generation := suite.installedPackageGeneration("test-driver-manifest-only")
	suite.DirExists(generation)
	externalLibrary := filepath.Join(suite.Dir(), "test_driver")
	suite.Require().NoError(os.WriteFile(externalLibrary, []byte("external library"), 0o644))
	externalSibling := filepath.Join(suite.Dir(), "external-sibling.txt")
	suite.Require().NoError(os.WriteFile(externalSibling, []byte("keep"), 0o644))

	// Now uninstall and verify we clean up
	m = UninstallCmd{Driver: "test-driver-manifest-only", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r ", "Driver `test-driver-manifest-only` uninstalled successfully!", suite.runCmd(m))
	suite.driverIsNotInstalled("test-driver-manifest-only")
	suite.NoDirExists(generation)
	suite.FileExists(externalLibrary)
	suite.FileExists(externalSibling)
}

func (suite *SubcommandTestSuite) installedPackageGeneration(runtimeID string) string {
	entries, err := os.ReadDir(suite.Dir())
	suite.Require().NoError(err)
	prefix := ".dbc-package-g-" + strconv.Itoa(len([]byte(runtimeID))) + "-" + runtimeID + "-"
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			return filepath.Join(suite.Dir(), entry.Name())
		}
	}
	suite.FailNow("receipt-backed package generation not found for " + runtimeID)
	return ""
}

// See https://github.com/columnar-tech/dbc/issues/37
func (suite *SubcommandTestSuite) TestUninstallInvalidManifest() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	m := InstallCmd{Driver: "test-driver-invalid-manifest", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.runCmd(m)
	suite.FileExists(filepath.Join(suite.Dir(), "test-driver-invalid-manifest.toml"))

	// A valid receipt identifies the package generation even though the manifest's
	// external shared-library reference is malformed for normal loading.
	generation := suite.installedPackageGeneration("test-driver-invalid-manifest")
	suite.FileExists(filepath.Join(generation, "libadbc_driver_invalid_manifest.so"))
	externalLibrary := filepath.Join(suite.Dir(), "libadbc_driver_invalid_manifest.so")
	suite.Require().NoError(os.WriteFile(externalLibrary, []byte("external library"), 0o644))

	m = UninstallCmd{Driver: "test-driver-invalid-manifest", Level: suite.configLevel}.GetModel()
	output := suite.runCmd(m)

	suite.validateOutput("\r ", "Driver `test-driver-invalid-manifest` uninstalled successfully!", output)

	// Ensure we don't nuke the installation directory which is the original (major) issue
	suite.DirExists(suite.Dir())

	// We do remove the manifest
	suite.NoFileExists(filepath.Join(suite.Dir(), "test-driver-invalid-manifest.toml"))
	// The owned package generation is removed, while the external shared reference is preserved.
	suite.NoDirExists(generation)
	suite.FileExists(externalLibrary)
}

func (suite *SubcommandTestSuite) TestUninstallRemovesSymlink() {
	if runtime.GOOS == "windows" && (suite.configLevel == config.ConfigUser || suite.configLevel == config.ConfigSystem) {
		suite.T().Skip("Symlinks aren't created on Windows for User and System config levels")
	}

	// Install a driver
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	_ = suite.runCmd(m)
	suite.driverIsInstalled("test-driver-1", true)

	// Verify symlink is in place in the parent dir and is actually a symlink
	manifestPath := filepath.Join(suite.Dir(), "..", "test-driver-1.toml")
	suite.FileExists(manifestPath)
	info, err := os.Lstat(manifestPath)
	suite.NoError(err)
	suite.Equal(os.ModeSymlink, info.Mode()&os.ModeSymlink, "Expected test-driver-1.toml to be a symlink")

	// Uninstall the driver
	m = UninstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.GetModel()
	_ = suite.runCmd(m)

	// Verify symlink is gone
	suite.NoFileExists(manifestPath)
}

func (suite *SubcommandTestSuite) TestUninstall_JSON() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	m = UninstallCmd{Driver: "test-driver-1", Level: suite.configLevel, Json: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	var env jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(out), &env), "output must be valid JSON: %s", out)
	suite.Equal(1, env.SchemaVersion)
	suite.Equal("uninstall.status", env.Kind)

	var status jsonschema.UninstallStatus
	suite.Require().NoError(json.Unmarshal(env.Payload, &status))
	suite.Equal("success", status.Status)
	suite.Equal("test-driver-1", status.Driver)
}
