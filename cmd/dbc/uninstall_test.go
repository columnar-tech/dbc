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
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
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

func (suite *SubcommandTestSuite) TestUninstallExternalRegistrationPreservesSharedLibrary() {
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
	suite.NoFileExists(path.Join(suite.tempdir, "found.toml"))
	suite.FileExists(path.Join(pkgdir, "some.dll"))
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

func (suite *SubcommandTestSuite) TestUninstallLegacyPackageRemovesExactPayloadAndPreservesExternalFiles() {
	if runtime.GOOS == "windows" {
		suite.T().Skip()
	}

	suite.Require().NoError(os.MkdirAll(suite.Dir(), 0o755))
	packageDir := filepath.Join(suite.Dir(), "legacy-test-driver-install")
	suite.Require().NoError(os.Mkdir(packageDir, 0o755))
	legacyLibrary := filepath.Join(packageDir, "libadbc_driver_invalid_manifest.so")
	suite.Require().NoError(os.WriteFile(legacyLibrary, []byte("legacy package library"), 0o644))
	externalLibrary := filepath.Join(suite.Dir(), "libadbc_driver_invalid_manifest.so")
	suite.Require().NoError(os.WriteFile(externalLibrary, []byte("external library"), 0o644))
	info := config.DriverInfo{
		ID:      "test-driver-invalid-manifest",
		Name:    "Legacy Test Driver",
		Version: semver.MustParse("1.0.0"),
		Source:  "dbc",
	}
	info.Driver.Shared.Set(config.PlatformTuple(), legacyLibrary)
	suite.Require().NoError(config.CreateManifest(config.Config{Level: suite.configLevel, Location: suite.Dir()}, info))

	m := UninstallCmd{Driver: "test-driver-invalid-manifest", Level: suite.configLevel}.GetModel()
	output := suite.runCmd(m)

	suite.validateOutput("\r ", "Driver `test-driver-invalid-manifest` uninstalled successfully!", output)

	suite.DirExists(suite.Dir())
	suite.NoFileExists(filepath.Join(suite.Dir(), "test-driver-invalid-manifest.toml"))
	suite.NoDirExists(packageDir)
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

func TestUninstallCleanupErrorShowsRetainedGenerationPath(t *testing.T) {
	generation := filepath.Join(t.TempDir(), ".dbc-package-g-6-driver-abc123")
	cleanupErr := fmt.Errorf("driver registration was removed, but package cleanup failed; package files may remain under %s: remove package generation payload %s: permission denied",
		filepath.Dir(generation), generation)
	model := uninstallModel{}
	updated, _ := model.Update(fmt.Errorf("failed to uninstall driver: %v", cleanupErr))

	status, ok := updated.(HasStatus)
	if !ok || status.Status() != 1 {
		t.Fatalf("uninstall model status = %v, want failure", updated)
	}
	visibleError := formatErr(status.Err())
	if !strings.Contains(visibleError, generation) {
		t.Fatalf("user-visible uninstall error %q does not include retained generation path %q", visibleError, generation)
	}
}
