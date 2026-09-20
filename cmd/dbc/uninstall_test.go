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
	"strings"

	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/pelletier/go-toml/v2"
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

	// Verify the receipt-owned package generation exists before uninstall.
	entries, err := os.ReadDir(suite.Dir())
	suite.Require().NoError(err)
	var generationPath string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".dbc-package-test-driver-manifest-only-") {
			generationPath = filepath.Join(suite.Dir(), entry.Name())
			break
		}
	}
	suite.Require().NotEmpty(generationPath)
	suite.DirExists(generationPath)
	externalLibrary := filepath.Join(suite.Dir(), "test_driver")
	suite.Require().NoError(os.WriteFile(externalLibrary, []byte("shared external library"), 0o644))

	// Now uninstall and verify we clean up
	m = UninstallCmd{Driver: "test-driver-manifest-only", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r ", "Driver `test-driver-manifest-only` uninstalled successfully!", suite.runCmd(m))
	suite.driverIsNotInstalled("test-driver-manifest-only")
	suite.NoDirExists(generationPath)
	externalContents, err := os.ReadFile(externalLibrary)
	suite.Require().NoError(err)
	suite.Equal("shared external library", string(externalContents))
}

// See https://github.com/columnar-tech/dbc/issues/37
func (suite *SubcommandTestSuite) TestUninstallInvalidManifest() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	m := InstallCmd{Driver: "test-driver-invalid-manifest", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.runCmd(m)
	manifestPath := filepath.Join(suite.Dir(), "test-driver-invalid-manifest.toml")
	suite.FileExists(manifestPath)

	// The legacy package explicitly declares a relative runtime load path. It must
	// remain separate from the managed package generation.
	type partialManifest struct {
		Driver struct {
			Shared map[string]string `toml:"shared"`
		}
	}
	var invalidManifest partialManifest
	f, err := os.Open(filepath.Join(suite.Dir(), "test-driver-invalid-manifest.toml"))
	suite.Require().NoError(err)
	err = toml.NewDecoder(f).Decode(&invalidManifest)
	suite.Require().NoError(err)
	suite.Require().NoError(f.Close())
	value := invalidManifest.Driver.Shared[config.PlatformTuple()]
	suite.Equal("libadbc_driver_invalid_manifest.so", value)
	externalLibrary := filepath.Join(suite.Dir(), value)
	suite.Require().NoError(os.WriteFile(externalLibrary, []byte("external library"), 0o644))
	packageEntries, err := os.ReadDir(suite.Dir())
	suite.Require().NoError(err)
	runtimeManifest, err := os.ReadFile(manifestPath)
	suite.Require().NoError(err)
	suite.Contains(string(runtimeManifest), value)

	m = UninstallCmd{Driver: "test-driver-invalid-manifest", Level: suite.configLevel}.GetModel()
	output := suite.runCmd(m)

	suite.validateOutput("\r ", "Driver `test-driver-invalid-manifest` uninstalled successfully!", output)

	// Ensure we don't nuke the installation directory which is the original (major) issue.
	suite.DirExists(suite.Dir())

	// We do remove the manifest
	suite.NoFileExists(filepath.Join(suite.Dir(), "test-driver-invalid-manifest.toml"))
	// The receipt-owned package is removed, while the external runtime library is
	// retained because it is not inside that package generation.
	for _, entry := range packageEntries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".dbc-package-test-driver-invalid-manifest-") {
			suite.NoDirExists(filepath.Join(suite.Dir(), entry.Name()))
		}
	}
	externalContents, err := os.ReadFile(externalLibrary)
	suite.Require().NoError(err)
	suite.Equal("external library", string(externalContents))
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
