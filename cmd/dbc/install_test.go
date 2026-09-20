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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

func (suite *SubcommandTestSuite) TestInstall() {
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	out := suite.runCmd(m)

	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.1.0 to "+suite.Dir(), out)
	suite.driverIsInstalled("test-driver-1", true)
}

func (suite *SubcommandTestSuite) TestInstallDriverNotFound() {
	m := InstallCmd{Driver: "foo", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r ", "\nError: could not find driver: driver `foo` not found in driver registry index; try: `dbc search` to list available drivers", suite.runCmdErr(m))
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
				GetModelCustom(testBaseModel())
			out := suite.runCmd(m)

			suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
				"\nInstalled test-driver-1 "+tt.expectedVersion+" to "+suite.Dir(), out)
			suite.driverIsInstalled("test-driver-1", true)
			m = UninstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.GetModelCustom(
				testBaseModel())
			suite.runCmd(m)
		})
	}
}

func (suite *SubcommandTestSuite) TestInstallWithVersionLessSpace() {
	m := InstallCmd{Driver: "test-driver-1 < 1.1.0"}.
		GetModelCustom(testBaseModel())
	out := suite.runCmd(m)
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.0.0 to "+suite.tempdir, out)
}

func (suite *SubcommandTestSuite) TestReinstallUpdateVersion() {
	m := InstallCmd{Driver: "test-driver-1<=1.0.0"}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.0.0 to "+suite.tempdir, suite.runCmd(m))

	m = InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nRemoved conflicting driver: test-driver-1 (version: 1.0.0)\nInstalled test-driver-1 1.1.0 to "+suite.tempdir,
		suite.runCmd(m))

	installed, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: suite.Dir()}, "test-driver-1")
	suite.Require().NoError(err)
	libraryPath := installed.Driver.Shared.Get(config.PlatformTuple())
	relLibrary, err := filepath.Rel(suite.Dir(), libraryPath)
	suite.Require().NoError(err)
	relDir := filepath.ToSlash(filepath.Dir(relLibrary))
	relLibrary = filepath.ToSlash(relLibrary)
	suite.Equal([]string{filepath.ToSlash(filepath.Join(relDir, "dbc-install-receipt.json")),
		relLibrary, relLibrary + ".sig", "test-driver-1.toml"}, suite.getFilesInTempDir())
}

func (suite *SubcommandTestSuite) TestReinstallDowngradeVersion() {
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.1.0 to "+suite.Dir(), suite.runCmd(m))
	suite.driverIsInstalledWithVersion("test-driver-1", "1.1.0", true)
	previous, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: suite.Dir()}, "test-driver-1")
	suite.Require().NoError(err)
	previousLibrary := previous.Driver.Shared.Get(config.PlatformTuple())

	m = InstallCmd{Driver: "test-driver-1<=1.0.0", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nRemoved conflicting driver: test-driver-1 (version: 1.1.0)\nInstalled test-driver-1 1.0.0 to "+suite.Dir(),
		suite.runCmd(m))

	files := suite.getFilesInDir(suite.Dir())
	installed, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: suite.Dir()}, "test-driver-1")
	suite.Require().NoError(err)
	currentLibrary := installed.Driver.Shared.Get(config.PlatformTuple())
	currentRelative, err := filepath.Rel(suite.Dir(), currentLibrary)
	suite.Require().NoError(err)
	previousRelative, err := filepath.Rel(suite.Dir(), previousLibrary)
	suite.Require().NoError(err)
	suite.Contains(files, filepath.ToSlash(currentRelative))
	suite.Contains(files, filepath.ToSlash(currentRelative)+".sig")
	suite.NotContains(files, filepath.ToSlash(previousRelative))
	suite.driverIsInstalledWithVersion("test-driver-1", "1.0.0", true)
}

func (suite *SubcommandTestSuite) TestInstallVenv() {
	suite.T().Setenv("ADBC_DRIVER_PATH", "")
	suite.T().Setenv("VIRTUAL_ENV", suite.tempdir)

	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.1.0 to "+filepath.Join(suite.tempdir, "etc", "adbc", "drivers"), suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestInstallEnvironmentPrecedence() {
	// Like the driver managers, dbc follows a precedence chain when
	// ADBC_DRIVER_MANAGER, VIRTUAL_ENV, and CONDA_PREFIX are set with each
	// variable overriding the next.
	driver_path := filepath.Join(suite.tempdir, "driver_path")
	venv_path := filepath.Join(suite.tempdir, "venv_path")
	conda_path := filepath.Join(suite.tempdir, "conda_path")

	suite.T().Setenv("ADBC_DRIVER_PATH", driver_path)
	suite.T().Setenv("VIRTUAL_ENV", venv_path)
	suite.T().Setenv("CONDA_PREFIX", conda_path)

	m := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	suite.runCmd(m)

	suite.FileExists(filepath.Join(driver_path, "test-driver-1.toml"))
	suite.NoFileExists(filepath.Join(venv_path, "test-driver-1.toml"))
	suite.NoFileExists(filepath.Join(conda_path, "test-driver-1.toml"))

	suite.T().Setenv("ADBC_DRIVER_PATH", "")
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	suite.runCmd(m)
	suite.FileExists(filepath.Join(venv_path, "etc", "adbc", "drivers", "test-driver-1.toml"))
	suite.NoFileExists(filepath.Join(conda_path, "etc", "adbc", "drivers", "test-driver-1.toml"))

	suite.T().Setenv("VIRTUAL_ENV", "")
	m = InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	suite.runCmd(m)
	suite.FileExists(filepath.Join(conda_path, "etc", "adbc", "drivers", "test-driver-1.toml"))
}

func (suite *SubcommandTestSuite) TestInstallCondaPrefix() {
	suite.T().Setenv("ADBC_DRIVER_PATH", "")
	suite.T().Setenv("CONDA_PREFIX", suite.tempdir)

	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.1.0 to "+filepath.Join(suite.tempdir, "etc", "adbc", "drivers"), suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestInstallManifestOnlyDriver() {
	m := InstallCmd{Driver: "test-driver-manifest-only", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())

	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-manifest-only 1.0.0 to "+suite.Dir()+
			"\n\nMust have libtest_driver installed to load this driver", suite.runCmd(m))
	suite.driverIsInstalled("test-driver-manifest-only", false)
}

func (suite *SubcommandTestSuite) TestInstallDriverNoSignature() {
	m := InstallCmd{Driver: "test-driver-no-sig"}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)
	suite.Contains(out, "signature file 'test-driver-1-not-valid.so.sig' for driver is missing")

	suite.Empty(suite.getFilesInTempDir())
	suite.NoDirExists(filepath.Join(suite.tempdir, "test-driver-no-sig"))

	// Note: The UI output (first parameter) serves as documentation but isn't verified
	// by validateOutput due to tea.WithoutRenderer() mode. Manual verification needed.
	m = InstallCmd{Driver: "test-driver-no-sig", NoVerify: true}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[-] verifying signature\r\n",
		"\nInstalled test-driver-no-sig 1.1.0 to "+suite.tempdir, suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestInstallGitignoreDefaultBehavior() {
	driver_path := filepath.Join(suite.tempdir, "driver_path")
	ignorePath := filepath.Join(driver_path, ".gitignore")
	suite.T().Setenv("ADBC_DRIVER_PATH", driver_path)

	suite.NoFileExists(ignorePath)

	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(testBaseModel())
	_ = suite.runCmd(m)

	suite.FileExists(ignorePath)
}

func (suite *SubcommandTestSuite) TestInstallGitignoreExistingDir() {
	driver_path := filepath.Join(suite.tempdir, "driver_path")
	ignorePath := filepath.Join(driver_path, ".gitignore")
	suite.T().Setenv("ADBC_DRIVER_PATH", driver_path)

	// Create the directory before we install the driver
	mkdirerr := os.MkdirAll(driver_path, 0o755)
	if mkdirerr != nil {
		suite.Error(mkdirerr)
	}

	suite.DirExists(driver_path)
	suite.NoFileExists(ignorePath)

	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(testBaseModel())
	_ = suite.runCmd(m)

	// There shouldn't be a .gitignore because we didn't create the dir fresh
	// during install
	suite.NoFileExists(ignorePath)
}

func (suite *SubcommandTestSuite) TestInstallGitignorePreserveUserModified() {
	driver_path := filepath.Join(suite.tempdir, "driver_path")
	ignorePath := filepath.Join(driver_path, ".gitignore")
	suite.T().Setenv("ADBC_DRIVER_PATH", driver_path)

	suite.NoFileExists(ignorePath)

	// First install - should create .gitignore
	m := InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(testBaseModel())
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
		GetModelCustom(testBaseModel())
	_ = suite.runCmd(m)
	m = InstallCmd{Driver: "test-driver-1"}.
		GetModelCustom(testBaseModel())
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
		GetModelCustom(testBaseModel())
	_ = suite.runCmd(m)
	suite.driverIsInstalled("test-driver-1", true)

	// Verify symlink is in place in the parent dir and is actually a symlink
	manifestPath := filepath.Join(suite.Dir(), "..", "test-driver-1.toml")
	suite.FileExists(manifestPath)
	info, err := os.Lstat(manifestPath)
	suite.NoError(err)
	suite.Equal(os.ModeSymlink, info.Mode()&os.ModeSymlink, "Expected test-driver-1.toml to be a symlink")
}

func (suite *SubcommandTestSuite) TestInstallLocalPackage() {
	packagePath := filepath.Join("testdata", "test-driver-1.tar.gz")
	m := InstallCmd{Driver: packagePath, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	out := suite.runCmd(m)

	suite.validateOutput("Installing from local package: "+packagePath+"\r\n\r\n\r"+
		"[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.0.0 to "+suite.Dir(), out)
	suite.driverIsInstalled("test-driver-1", true)
}

func (suite *SubcommandTestSuite) TestInstallLocalPackageNotFound() {
	packagePath := filepath.Join("testdata", "test-driver-2.tar.gz")
	m := InstallCmd{Driver: packagePath, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)

	errmsg := "no such file or directory"
	if runtime.GOOS == "windows" {
		errmsg = "The system cannot find the file specified."
	}
	suite.validateOutput("Installing from local package: "+packagePath+
		"\r\n\r\n\r ", "\nError: open "+packagePath+": "+errmsg, out)
	suite.driverIsNotInstalled("test-driver-2")
}

func (suite *SubcommandTestSuite) TestInstallLocalPackageNoSignature() {
	packagePath := filepath.Join("testdata", "test-driver-no-sig.tar.gz")
	m := InstallCmd{Driver: packagePath}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)
	suite.Contains(out, "signature file 'test-driver-1-not-valid.so.sig' for driver is missing")

	suite.Empty(suite.getFilesInTempDir())
	suite.NoDirExists(filepath.Join(suite.tempdir, "test-driver-no-sig"))

	m = InstallCmd{Driver: packagePath, NoVerify: true}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("Installing from local package: "+packagePath+"\r\n\r\n\r"+
		"[✓] installing\r\n[-] verifying signature\r\n",
		"\nInstalled test-driver-no-sig 1.1.0 to "+suite.tempdir, suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestInstallLocalPackageFixUpName() {
	origPackagePath, err := filepath.Abs(filepath.Join("testdata", "test-driver-1.tar.gz"))
	suite.Require().NoError(err)
	packagePath := filepath.Join(suite.tempdir, "test-driver-1_"+config.PlatformTuple()+"_v1.0.0.tgz")
	suite.Require().NoError(os.Symlink(origPackagePath, packagePath))
	m := InstallCmd{Driver: packagePath, Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	out := suite.runCmd(m)

	suite.validateOutput("Installing from local package: "+packagePath+"\r\n\r\n\r"+
		"[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.0.0 to "+suite.Dir(), out)
	suite.driverIsInstalled("test-driver-1", true)
}

func (suite *SubcommandTestSuite) TestInstallWithPreOnlyPrereleaseDriver() {
	// Install test-driver-only-pre with --pre flag, should succeed
	m := InstallCmd{Driver: "test-driver-only-pre", Level: suite.configLevel, Pre: true}.
		GetModelCustom(testBaseModel())
	out := suite.runCmd(m)

	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-only-pre 0.9.0-alpha.1 to "+suite.Dir(), out)
	suite.driverIsInstalled("test-driver-only-pre", false)
}

func (suite *SubcommandTestSuite) TestInstallWithoutPreOnlyPrereleaseDriver() {
	// Try to install test-driver-only-pre without --pre flag, should fail
	m := InstallCmd{Driver: "test-driver-only-pre", Level: suite.configLevel, Pre: false}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)

	suite.Contains(out, "driver `test-driver-only-pre` not found")
	suite.Contains(out, "but prerelease versions filtered out")
	suite.Contains(out, "try: dbc install --pre test-driver-only-pre")
	suite.driverIsNotInstalled("test-driver-only-pre")
}

func (suite *SubcommandTestSuite) TestInstallWithoutPreWhenPrereleaseAlreadyInstalled() {
	m := InstallCmd{Driver: "test-driver-only-pre", Level: suite.configLevel, Pre: true}.
		GetModelCustom(testBaseModel())
	suite.runCmd(m)
	suite.driverIsInstalled("test-driver-only-pre", false)

	m = InstallCmd{Driver: "test-driver-only-pre", Level: suite.configLevel, Pre: false}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)

	suite.Contains(out, "already installed")
	suite.Contains(out, "0.9.0-alpha.1")
	suite.Contains(out, "dbc install --pre test-driver-only-pre")
}

func (suite *SubcommandTestSuite) TestInstallExplicitPrereleaseWithoutPreFlag() {
	// Install explicit prerelease version WITHOUT --pre flag, should succeed per requirement
	m := InstallCmd{Driver: "test-driver-only-pre=0.9.0-alpha.1", Level: suite.configLevel, Pre: false}.
		GetModelCustom(testBaseModel())
	out := suite.runCmd(m)

	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-only-pre 0.9.0-alpha.1 to "+suite.Dir(), out)
	suite.driverIsInstalled("test-driver-only-pre", false)
}

func (suite *SubcommandTestSuite) TestInstallPartialRegistryFailure() {
	// Test that install command handles partial registry failure gracefully
	// (one registry succeeds, another fails - returns both drivers and error)
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://secondary-registry.example.com: failed to fetch driver registry: network error")
	}

	// Should succeed if the requested driver is found in the available drivers
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	// Should install successfully without printing the registry error
	suite.Contains(out, "Installed test-driver-1 1.1.0")
	suite.driverIsInstalled("test-driver-1", true)
}

func (suite *SubcommandTestSuite) TestInstallPartialRegistryFailureDriverNotFound() {
	// Test that install command shows registry errors when the requested driver is not found
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://secondary-registry.example.com: failed to fetch driver registry: network error")
	}

	// Should fail with enhanced error message if the requested driver is not found
	m := InstallCmd{Driver: "nonexistent-driver", Level: suite.configLevel}.
		GetModelCustom(baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmdErr(m)

	// Should show the driver not found error AND the registry error
	suite.Contains(out, "could not find driver")
	suite.Contains(out, "nonexistent-driver")
	suite.Contains(out, "Note: Some driver registries were unavailable")
	suite.Contains(out, "failed to fetch driver registry")
	suite.Contains(out, "network error")
}

func (suite *SubcommandTestSuite) TestInstallCompleteRegistryFailure() {
	// Test that install command handles complete registry failure (no drivers returned)
	completeFailingRegistry := func() ([]dbc.Driver, error) {
		return nil, fmt.Errorf("registry https://primary-registry.example.com: connection timeout")
	}

	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(baseModel{getDriverRegistry: completeFailingRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmdErr(m)

	suite.Contains(out, "connection timeout")
	suite.driverIsNotInstalled("test-driver-1")
}

func (suite *SubcommandTestSuite) TestInstallDriverWithSubdirectories() {
	packageDir := suite.T().TempDir()
	packagePath := filepath.Join(packageDir, "driver-with-subdir.tar.gz")

	f, err := os.Create(packagePath)
	suite.Require().NoError(err)
	gzw := gzip.NewWriter(f)
	tw := tar.NewWriter(gzw)

	// Just add the subdir as the only entry
	err = tw.WriteHeader(&tar.Header{
		Name:     "subdir/",
		Mode:     0755,
		Typeflag: tar.TypeDir,
	})
	suite.Require().NoError(err)

	suite.Require().NoError(tw.Close())
	suite.Require().NoError(gzw.Close())
	suite.Require().NoError(f.Close())

	// Should fail
	m := InstallCmd{Driver: packagePath, NoVerify: true}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)

	// and return an error with this
	suite.Contains(out, "driver archives must be flat")
}

func packageV2ArchiveForInstall(t *testing.T, id, version, platform string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	manifest := fmt.Sprintf(`package_version = 2
id = %q
name = "Example Driver"
version = %q
platform = %q

[Driver]
entrypoint = "AdbcDriverExampleInit"

[Files]
driver = "driver.so"
`, id, version, platform)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"MANIFEST", []byte(manifest)}, {"driver.so", []byte("driver library")}} {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func openInstallArchive(t *testing.T, data []byte) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "package-*.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestInstallSignatureFailurePreservesExistingInstallation(t *testing.T) {
	oldArchive, err := os.ReadFile(filepath.Join("testdata", "test-driver-1.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	oldExpected := config.ExpectedPackageMetadata{
		ID: "test-driver-1", Version: "1.0.0", Platform: config.PlatformTuple(),
		SourceType: "registry", SourceIdentity: testRegistry.BaseURL.String(),
	}
	root := t.TempDir()
	cfg := config.Config{Level: config.ConfigEnv, Location: root}
	oldManifest, err := config.InstallPackage(cfg, "test-driver-1", openInstallArchive(t, oldArchive), oldExpected, config.InstallOptions{
		Verify: func(stagingDir string, manifest config.Manifest) error {
			return dbc.VerifyPackageSignature(stagingDir, manifest)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	oldManifestBytes, err := os.ReadFile(filepath.Join(root, "test-driver-1.toml"))
	if err != nil {
		t.Fatal(err)
	}
	oldLibraryPath := oldManifest.Driver.Shared.Get(config.PlatformTuple())
	oldLibraryBytes, err := os.ReadFile(oldLibraryPath)
	if err != nil {
		t.Fatal(err)
	}

	badArchive, err := os.ReadFile(filepath.Join("testdata", "test-driver-no-sig.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	model := progressiveInstallModel{
		Driver: "test-driver-1",
		cfg:    cfg,
		DriverPackage: dbc.PkgInfo{
			Driver:        dbc.Driver{Path: "test-driver-1", Registry: &testRegistry},
			Version:       semver.MustParse("1.1.0"),
			PlatformTuple: config.PlatformTuple(),
		},
	}
	_, install := model.startInstalling(openInstallArchive(t, badArchive))
	message := install()
	installErr, ok := message.(error)
	if !ok {
		t.Fatalf("startInstalling returned %T, want signature error", message)
	}
	if !strings.Contains(installErr.Error(), "signature file 'test-driver-1-not-valid.so.sig' for driver is missing") {
		t.Fatalf("unexpected install error: %v", installErr)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(root, "test-driver-1.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldManifestBytes, manifestBytes) {
		t.Fatal("runtime manifest changed after signature failure")
	}
	libraryBytes, err := os.ReadFile(oldLibraryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldLibraryBytes, libraryBytes) {
		t.Fatal("installed library changed after signature failure")
	}
}

func TestStartInstallingRegistryPackageV2RequiresAndUsesMetadata(t *testing.T) {
	archive := packageV2ArchiveForInstall(t, "example", "1.2.3", config.PlatformTuple())
	digest := sha256.Sum256(archive)
	size := int64(len(archive))
	baseURL, err := url.Parse("https://registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	model := progressiveInstallModel{
		Driver: "example",
		cfg:    config.Config{Level: config.ConfigEnv, Location: root},
		DriverPackage: dbc.PkgInfo{
			Driver:        dbc.Driver{Path: "example", Registry: &dbc.Registry{BaseURL: baseURL}},
			Version:       semver.MustParse("1.2.3"),
			PlatformTuple: config.PlatformTuple(),
			ArtifactHash:  "sha256:" + hex.EncodeToString(digest[:]),
			ArtifactSize:  &size,
		},
	}
	_, install := model.startInstalling(openInstallArchive(t, archive))
	message := install()
	manifest, ok := message.(config.Manifest)
	if !ok {
		t.Fatalf("startInstalling returned %T, want config.Manifest", message)
	}
	if manifest.PackageVersion != 2 {
		t.Fatalf("package marker = %d, want 2", manifest.PackageVersion)
	}
	if err := dbc.VerifyPackageSignature("", manifest); err != nil {
		t.Fatalf("package v2 must not require legacy PGP signature: %v", err)
	}
	var receipt config.InstallReceipt
	receiptData, err := os.ReadFile(filepath.Join(filepath.Dir(manifest.Driver.Shared.Get(config.PlatformTuple())), "dbc-install-receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(receiptData, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.SourceType != "registry" || receipt.SourceIdentity != baseURL.String() {
		t.Fatalf("receipt source = %q %q, want registry %q", receipt.SourceType, receipt.SourceIdentity, baseURL)
	}
	if receipt.ArchiveHash != "sha256:"+hex.EncodeToString(digest[:]) || receipt.ArchiveSize != size {
		t.Fatalf("receipt archive metadata = %q/%d, want %q/%d", receipt.ArchiveHash, receipt.ArchiveSize, "sha256:"+hex.EncodeToString(digest[:]), size)
	}
}

func TestStartInstallingRegistryV2WithoutArchiveMetadataIsRejected(t *testing.T) {
	archive := packageV2ArchiveForInstall(t, "example", "1.2.3", config.PlatformTuple())
	root := t.TempDir()
	model := progressiveInstallModel{
		Driver: "example",
		cfg:    config.Config{Level: config.ConfigEnv, Location: root},
		DriverPackage: dbc.PkgInfo{
			Driver:        dbc.Driver{Path: "example", Registry: &dbc.Registry{BaseURL: must(url.Parse("https://registry.example.test"))}},
			Version:       semver.MustParse("1.2.3"),
			PlatformTuple: config.PlatformTuple(),
		},
	}
	_, install := model.startInstalling(openInstallArchive(t, archive))
	message := install()
	installErr, ok := message.(error)
	if !ok || !strings.Contains(installErr.Error(), "requires archive hash and size metadata") {
		t.Fatalf("startInstalling returned %v, want missing metadata error", message)
	}
	if _, err := os.Stat(filepath.Join(root, "example.toml")); !os.IsNotExist(err) {
		t.Fatalf("runtime manifest was published despite missing registry metadata: %v", err)
	}
}

func TestStartInstallingLocalV2UsesManifestID(t *testing.T) {
	archive := packageV2ArchiveForInstall(t, "declared-driver", "1.2.3", config.PlatformTuple())
	root := t.TempDir()
	packagePath := filepath.Join(root, "filename-driver.tar.gz")
	if err := os.WriteFile(packagePath, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	downloaded, err := os.Open(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	model := progressiveInstallModel{
		Driver:           packagePath,
		isLocal:          true,
		localPackagePath: packagePath,
		cfg:              config.Config{Level: config.ConfigEnv, Location: root},
	}
	_, install := model.startInstalling(downloaded)
	message := install()
	manifest, ok := message.(config.Manifest)
	if !ok {
		t.Fatalf("startInstalling returned %T, want config.Manifest", message)
	}
	if manifest.ID != "declared-driver" {
		t.Fatalf("installed runtime ID = %q, want MANIFEST ID", manifest.ID)
	}
	if _, err := os.Stat(filepath.Join(root, "declared-driver.toml")); err != nil {
		t.Fatalf("runtime manifest for MANIFEST ID was not registered: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "filename-driver.toml")); !os.IsNotExist(err) {
		t.Fatalf("filename-derived runtime manifest was unexpectedly registered: %v", err)
	}
}

func TestStartInstallingRejectsPartialRegistryArchiveMetadata(t *testing.T) {
	model := progressiveInstallModel{
		Driver: "example",
		DriverPackage: dbc.PkgInfo{
			Driver:        dbc.Driver{Path: "example", Registry: &dbc.Registry{BaseURL: must(url.Parse("https://registry.example.test"))}},
			Version:       semver.MustParse("1.2.3"),
			PlatformTuple: config.PlatformTuple(),
			ArtifactHash:  "sha256:" + strings.Repeat("0", 64),
		},
		cfg: config.Config{Level: config.ConfigEnv, Location: t.TempDir()},
	}
	_, install := model.startInstalling(openInstallArchive(t, packageV2ArchiveForInstall(t, "example", "1.2.3", config.PlatformTuple())))
	message := install()
	installErr, ok := message.(error)
	if !ok || !strings.Contains(installErr.Error(), "must include both archive hash and size") {
		t.Fatalf("startInstalling returned %v, want partial metadata error", message)
	}
}

func (suite *SubcommandTestSuite) TestInstallJSON() {
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, Json: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	lastLine := lines[len(lines)-1]
	var env jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(lastLine), &env), "last output line must be valid JSON: %s", lastLine)

	suite.Equal(1, env.SchemaVersion)
	suite.Equal("install.status", env.Kind)

	var status jsonschema.InstallStatus
	suite.Require().NoError(json.Unmarshal(env.Payload, &status))

	suite.Equal("installed", status.Status)
	suite.Equal("test-driver-1", status.Driver)
	suite.NotEmpty(status.Version)
	suite.NotEmpty(status.Location)
}

func (suite *SubcommandTestSuite) TestInstall_ChecksumInStatus() {
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, Json: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	suite.Greater(len(lines), 0)

	lastLine := lines[len(lines)-1]
	var env jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(lastLine), &env))
	suite.Equal("install.status", env.Kind)

	var status jsonschema.InstallStatus
	suite.Require().NoError(json.Unmarshal(env.Payload, &status))
	suite.Equal("installed", status.Status)
	// Checksum should be present as a bare hex string (no prefix)
	suite.NotEmpty(status.Checksum, "expected checksum to be non-empty")
	suite.False(strings.HasPrefix(status.Checksum, "sha256:"), "expected bare hex checksum without sha256: prefix, got: %s", status.Checksum)
}

func (suite *SubcommandTestSuite) TestInstall_InsecureNoChecksumFlag() {
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, Json: true, InsecureNoChecksum: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	suite.Greater(len(lines), 0)

	lastLine := lines[len(lines)-1]
	var env jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(lastLine), &env))
	suite.Equal("install.status", env.Kind)

	var status jsonschema.InstallStatus
	suite.Require().NoError(json.Unmarshal(env.Payload, &status))
	suite.Equal("installed", status.Status)
	// Checksum should be absent when --insecure-no-checksum is set
	suite.Empty(status.Checksum, "expected no checksum when InsecureNoChecksum is set")
}

func (suite *SubcommandTestSuite) TestInstall_JSONProgressStream() {
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, JsonStreamProgress: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	suite.Greater(len(lines), 1, "expected multiple NDJSON lines")

	var kinds []string
	for _, line := range lines {
		if line == "" {
			continue
		}
		var env jsonschema.Envelope
		suite.Require().NoError(json.Unmarshal([]byte(line), &env), "line must be valid JSON: %s", line)
		suite.Equal(1, env.SchemaVersion)
		kinds = append(kinds, env.Kind)
	}

	suite.Contains(kinds, "install.progress")
	suite.Equal("install.status", kinds[len(kinds)-1])

	var hasDownloadStart bool
	for _, line := range lines {
		if strings.Contains(line, `"download.start"`) {
			hasDownloadStart = true
			break
		}
	}
	suite.True(hasDownloadStart, "expected download.start event")
}

// TestInstallJSON_AlreadyInstalledChecksumFailure is a regression test for the
// fix that gates FinalOutput() on m.status. When the driver binary is missing
// the checksum computation fails, the model exits with status 1, and
// FinalOutput() must not emit an install.status success envelope.
func (suite *SubcommandTestSuite) TestInstallJSON_AlreadyInstalledChecksumFailure() {
	// First install the driver normally.
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	// Locate and delete the shared library so checksum() will fail.
	cfg := config.Get()[suite.configLevel]
	driver, err := config.GetDriver(cfg, "test-driver-1")
	suite.Require().NoError(err)
	sharedPath := driver.Driver.Shared.Get(config.PlatformTuple())
	suite.Require().NotEmpty(sharedPath, "shared library path should not be empty")
	suite.Require().NoError(os.Remove(sharedPath))

	// Reinstall with --json. The already-installed path fires, but checksum
	// fails because the file is gone. runCmdErr now appends FinalOutput() so
	// the JSON error envelope is captured through the shared harness path,
	// matching how main.go emits it.
	m2 := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, Json: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmdErr(m2)

	// The combined output must contain the structured error envelope.
	suite.NotEmpty(out, "expected error output from install JSON error path")
	suite.NotContains(out, `"install.status"`, "must not emit success envelope when checksum fails")

	// Decode the envelope and assert the correct kind and code.
	// runCmdErr appends FinalOutput(), so the last non-empty line is the JSON.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var jsonLine string
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "{") {
			jsonLine = strings.TrimSpace(lines[i])
			break
		}
	}
	suite.NotEmpty(jsonLine, "expected a JSON line in output: %s", out)
	var errEnv jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(jsonLine), &errEnv), "must be valid JSON: %s", jsonLine)
	suite.Equal("error", errEnv.Kind, "expected kind=error")
	var errPayload jsonschema.ErrorResponse
	suite.Require().NoError(json.Unmarshal(errEnv.Payload, &errPayload))
	suite.Equal("install_failed", errPayload.Code, "expected install_failed error code")
}
