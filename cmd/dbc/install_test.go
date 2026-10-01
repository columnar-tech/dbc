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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/hostpath"
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
	m := InstallCmd{Driver: "test-driver-1<=1.0.0", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.0.0 to "+suite.Dir(), suite.runCmd(m))
	oldGeneration := filepath.Dir(suite.getInstalledDriver("test-driver-1").Driver.Shared.Get(config.PlatformTuple()))

	m = InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nReplaced active driver: test-driver-1 (version: 1.0.0)\nInstalled test-driver-1 1.1.0 to "+suite.Dir(),
		suite.runCmd(m))

	newGeneration := filepath.Dir(suite.getInstalledDriver("test-driver-1").Driver.Shared.Get(config.PlatformTuple()))
	suite.NotEqual(oldGeneration, newGeneration)
	suite.True(strings.HasPrefix(filepath.Base(newGeneration), fmt.Sprintf(".dbc-package-g-%d-test-driver-1-", len([]byte("test-driver-1")))))
	suite.DirExists(newGeneration)
	suite.NoDirExists(oldGeneration)
}

func (suite *SubcommandTestSuite) TestReinstallDowngradeVersion() {
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nInstalled test-driver-1 1.1.0 to "+suite.Dir(), suite.runCmd(m))
	suite.driverIsInstalledWithVersion("test-driver-1", "1.1.0", true)
	oldGeneration := filepath.Dir(suite.getInstalledDriver("test-driver-1").Driver.Shared.Get(config.PlatformTuple()))

	m = InstallCmd{Driver: "test-driver-1<=1.0.0", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nReplaced active driver: test-driver-1 (version: 1.1.0)\nInstalled test-driver-1 1.0.0 to "+suite.Dir(),
		suite.runCmd(m))

	newGeneration := filepath.Dir(suite.getInstalledDriver("test-driver-1").Driver.Shared.Get(config.PlatformTuple()))
	suite.NotEqual(oldGeneration, newGeneration)
	suite.True(strings.HasPrefix(filepath.Base(newGeneration), fmt.Sprintf(".dbc-package-g-%d-test-driver-1-", len([]byte("test-driver-1")))))
	suite.DirExists(newGeneration)
	suite.NoDirExists(oldGeneration)
	suite.driverIsInstalledWithVersion("test-driver-1", "1.0.0", true)
}

func (suite *SubcommandTestSuite) TestInstallShadowsLowerPriorityVersion() {
	secondary := filepath.Join(suite.T().TempDir(), "secondary")
	suite.Require().NoError(os.MkdirAll(secondary, 0o755))
	suite.T().Setenv("ADBC_DRIVER_PATH", secondary)

	archive, err := os.Open(filepath.Join("testdata", "test-driver-1.tar.gz"))
	suite.Require().NoError(err)
	_, err = config.InstallPackage(context.Background(), config.Config{Level: config.ConfigEnv, Location: secondary}, "test-driver-1", archive,
		config.InstallPackageOptions{Verifier: func(stagingDir string, manifest config.Manifest) error {
			return verifySignatureInStaging(stagingDir, manifest, false)
		}})
	suite.Require().NoError(err)
	secondaryDriver, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: secondary}, "test-driver-1")
	suite.Require().NoError(err)
	secondaryLibrary := secondaryDriver.Driver.Shared.Get(config.PlatformTuple())
	secondaryBytes, err := os.ReadFile(secondaryLibrary)
	suite.Require().NoError(err)

	suite.T().Setenv("ADBC_DRIVER_PATH", suite.tempdir+string(os.PathListSeparator)+secondary)
	primaryConfig := config.Config{Level: config.ConfigEnv, Location: suite.tempdir + string(os.PathListSeparator) + secondary}
	selectedConflict, err := config.GetDriver(primaryConfig, "test-driver-1")
	suite.Require().NoError(err)
	suite.Equal(secondary, selectedConflict.FilePath)
	suite.False(conflictIsInInstallRoot(selectedConflict, filepath.SplitList(primaryConfig.Location)[0], config.ConfigEnv))
	newInstall := InstallCmd{Driver: "test-driver-1", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	newInstallModel := newInstall.(progressiveInstallModel)
	newInstallModel.cfg = primaryConfig
	out := suite.runCmd(newInstallModel)
	suite.Contains(out, "Shadowed active driver: test-driver-1 (version: 1.0.0)")
	suite.NotContains(out, "Removed conflicting driver")

	active, err := config.GetDriver(config.Get()[config.ConfigEnv], "test-driver-1")
	suite.Require().NoError(err)
	suite.Equal("1.1.0", active.Version.String())
	activeLibrary := active.Driver.Shared.Get(config.PlatformTuple())
	relativeActiveLibrary, err := filepath.Rel(suite.tempdir, activeLibrary)
	suite.Require().NoError(err)
	suite.False(filepath.IsAbs(relativeActiveLibrary) || relativeActiveLibrary == ".." ||
		strings.HasPrefix(relativeActiveLibrary, ".."+string(os.PathSeparator)),
		"active library should be inside the primary root: %s", activeLibrary)
	suite.FileExists(activeLibrary)

	remainingSecondary, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: secondary}, "test-driver-1")
	suite.Require().NoError(err)
	suite.Equal("1.0.0", remainingSecondary.Version.String())
	remainingBytes, err := os.ReadFile(secondaryLibrary)
	suite.Require().NoError(err)
	suite.Equal(secondaryBytes, remainingBytes)
}

func TestConflictIsInInstallRoot(t *testing.T) {
	root := t.TempDir()
	conflict := config.DriverInfo{FilePath: root}
	if !conflictIsInInstallRoot(conflict, root, config.ConfigEnv) {
		t.Fatal("same install root should be reported as replaced")
	}

	otherRoot := filepath.Join(t.TempDir(), "secondary")
	if conflictIsInInstallRoot(conflict, otherRoot, config.ConfigEnv) {
		t.Fatal("a different install root should be reported as shadowed")
	}

	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err == nil {
		if !conflictIsInInstallRoot(conflict, alias, config.ConfigEnv) {
			t.Fatal("symlink aliases of the same install root should be reported as replaced")
		}
		if !sameInstallRoot(root, alias) {
			t.Fatal("symlink aliases of the same install root should have the same filesystem identity")
		}
	} else if !hostpath.IsWindows() {
		t.Fatalf("create install root symlink: %v", err)
	}

	caseRoot := filepath.Join(t.TempDir(), "CaseRoot")
	if err := os.Mkdir(caseRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	caseVariant := filepath.Join(filepath.Dir(caseRoot), "caseroot")
	caseInfo, caseErr := os.Stat(caseVariant)
	if originalInfo, err := os.Stat(caseRoot); err == nil && caseErr == nil && os.SameFile(originalInfo, caseInfo) {
		if !sameInstallRoot(caseRoot, caseVariant) {
			t.Fatal("case variants identifying the same directory should have the same filesystem identity")
		}
	}

	if got := primaryInstallRoot("", config.ConfigEnv); got != "" {
		t.Fatalf("empty env location primary root = %q, want empty", got)
	}
	location := "first" + string(hostpath.ListSeparator()) + "second"
	if got := primaryInstallRoot(location, config.ConfigEnv); got != "first" {
		t.Fatalf("primary root = %q, want first path-list entry", got)
	}

	if conflictIsInInstallRoot(config.DriverInfo{FilePath: `HKCU\SOFTWARE\ADBC\Drivers`}, root, config.ConfigSystem) {
		t.Fatal("a user registry registration should not be reported as replaced by a system install")
	}
	if !conflictIsInInstallRoot(config.DriverInfo{FilePath: `HKCU\SOFTWARE\ADBC\Drivers`}, root, config.ConfigUser) {
		t.Fatal("a user registry registration should be reported as replaced by a user install")
	}
}

func TestPrimaryInstallRootUsesConfigLevelPathContract(t *testing.T) {
	location := "first" + string(hostpath.ListSeparator()) + "second"
	want := "first"
	if runtime.GOOS == "js" {
		want = location
	}
	if got := primaryInstallRoot(location, config.ConfigEnv); got != want {
		t.Errorf("primary install root = %q, want %q", got, want)
	}
	for _, level := range []config.ConfigLevel{config.ConfigUser, config.ConfigSystem} {
		if got := primaryInstallRoot(location, level); got != location {
			t.Errorf("config level %s primary install root = %q, want unchanged location %q", level, got, location)
		}
	}
}

func (suite *SubcommandTestSuite) TestInstallSameVersionOnSecondaryRootSkipsDownload() {
	secondary := filepath.Join(suite.tempdir, "secondary")
	suite.Require().NoError(os.MkdirAll(secondary, 0o755))
	suite.T().Setenv("ADBC_DRIVER_PATH", secondary)

	oldInstall := InstallCmd{Driver: "test-driver-1=1.0.0", Level: config.ConfigEnv}.
		GetModelCustom(testBaseModel())
	suite.runCmd(oldInstall)

	suite.T().Setenv("ADBC_DRIVER_PATH", suite.tempdir+string(os.PathListSeparator)+secondary)
	downloads := 0
	base := baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloads++
			return downloadTestPkg(pkg)
		},
	}
	m := InstallCmd{Driver: "test-driver-1=1.0.0", Level: config.ConfigEnv}.
		GetModelCustom(base)
	out := suite.runCmd(m)

	suite.Contains(out, "already installed")
	suite.Zero(downloads, "same-version install on a secondary root should skip before download")
	active, err := config.GetDriver(config.Get()[config.ConfigEnv], "test-driver-1")
	suite.Require().NoError(err)
	activeLibrary := active.Driver.Shared.Get(config.PlatformTuple())
	relativeActiveLibrary, err := filepath.Rel(secondary, activeLibrary)
	suite.Require().NoError(err)
	suite.False(filepath.IsAbs(relativeActiveLibrary) || relativeActiveLibrary == ".." ||
		strings.HasPrefix(relativeActiveLibrary, ".."+string(os.PathSeparator)),
		"same-version install should keep using the secondary root: %s", activeLibrary)
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

func (suite *SubcommandTestSuite) TestInstallRejectsManifestOnlyDriverEvenWithSharedPath() {
	m := InstallCmd{Driver: "test-driver-manifest-only", Level: suite.configLevel, NoVerify: true}.
		GetModelCustom(testBaseModel())

	output := suite.runCmdErr(m)
	suite.Contains(output, "does not specify Files.driver")
	suite.NoFileExists(filepath.Join(suite.Dir(), "test-driver-manifest-only.toml"))
}

func (suite *SubcommandTestSuite) TestInstallDriverNoSignature() {
	m := InstallCmd{Driver: "test-driver-no-sig"}.
		GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)
	suite.Contains(out, "signature file 'test-driver-1-not-valid.so.sig' for driver is missing")

	suite.Equal(expectedFilesWithPersistentDriverLock("test-driver-no-sig"), suite.getFilesInTempDir())
	suite.assertPersistentDriverLockFile("test-driver-no-sig")
	suite.NoDirExists(filepath.Join(suite.tempdir, "test-driver-no-sig"))

	registryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/index.yaml") {
			_, _ = fmt.Fprint(w, installRegistryDriverIndex("test-driver-no-sig", "v1.1.0", "test-driver-no-sig.tar.gz"))
			return
		}
		http.NotFound(w, r)
	}))
	defer registryServer.Close()
	client, err := dbc.NewClient(dbc.WithBaseURL(registryServer.URL))
	suite.Require().NoError(err)
	downloads := 0
	base := baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			return client.Search(context.Background(), "")
		},
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloads++
			return downloadTestPkg(pkg)
		},
	}

	// Note: The UI output (first parameter) serves as documentation but isn't verified
	// by validateOutput due to tea.WithoutRenderer() mode. Manual verification needed.
	m = InstallCmd{Driver: "test-driver-no-sig", NoVerify: true}.
		GetModelCustom(base)
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[-] verifying signature\r\n",
		"\nInstalled test-driver-no-sig 1.1.0 to "+suite.tempdir, suite.runCmd(m))
	suite.Equal(1, downloads)

	downloads = 0
	m = InstallCmd{Driver: "test-driver-no-sig"}.GetModelCustom(base)
	out = suite.runCmd(m)
	suite.Contains(out, "already installed")
	suite.NotContains(out, "signature")
	suite.Zero(downloads, "same-version install should not download or reverify the package")
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

	suite.Equal(expectedFilesWithPersistentDriverLock("test-driver-no-sig"), suite.getFilesInTempDir())
	suite.assertPersistentDriverLockFile("test-driver-no-sig")
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
	suite.Contains(out, "driver archives shouldn't contain subdirectories")
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

func (suite *SubcommandTestSuite) TestInstall_NoVerifyJSONProgressKeepsVerificationEventsOrdered() {
	m := InstallCmd{Driver: "test-driver-no-sig", Level: suite.configLevel, NoVerify: true, JsonStreamProgress: true}.
		GetModelCustom(testBaseModel())
	out := suite.runCmd(m)

	startIndex := strings.Index(out, `"event":"verify.start"`)
	completeIndex := strings.Index(out, `"event":"verify.complete"`)
	suite.Require().GreaterOrEqual(startIndex, 0, "expected verify.start progress event")
	suite.Require().GreaterOrEqual(completeIndex, 0, "expected verify.complete progress event")
	suite.Less(startIndex, completeIndex, "verification progress events should stay ordered")
	suite.Contains(out, `"kind":"install.status"`)
}

func (suite *SubcommandTestSuite) TestInstallVerificationPhaseIsReportedBeforeVerifierCompletes() {
	model := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, JsonStreamProgress: true}.
		GetModelCustom(testBaseModel()).(progressiveInstallModel)
	verifyEntered := make(chan struct{})
	verifyRelease := make(chan struct{})
	verifyCompleted := make(chan struct{})
	verifierReleased := false
	defer func() {
		if !verifierReleased {
			close(verifyRelease)
		}
	}()
	model.verifyStagedPackage = func(string, config.Manifest, bool) error {
		close(verifyEntered)
		<-verifyRelease
		close(verifyCompleted)
		return nil
	}
	var progress bytes.Buffer
	model = model.WithJSONWriter(&progress).(progressiveInstallModel)

	states := make(chan installState, 8)
	observed := &installStateObservedModel{model: model, states: states}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output := &bytes.Buffer{}
	savedProg := prog
	defer func() { prog = savedProg }()
	program := tea.NewProgram(observed, tea.WithInput(nil), tea.WithOutput(output),
		tea.WithoutRenderer(), tea.WithContext(ctx))
	prog = program
	runDone := make(chan error, 1)
	go func() {
		_, err := program.Run()
		program.Wait()
		runDone <- err
	}()

	select {
	case <-verifyEntered:
	case <-ctx.Done():
		suite.FailNow("verifier was not entered before timeout")
	}

	stateReached := false
	for !stateReached {
		select {
		case state := <-states:
			stateReached = state == stVerifying
		case <-ctx.Done():
			suite.FailNow("install model did not enter the verifying state before timeout")
		}
	}
	select {
	case <-verifyCompleted:
		suite.FailNow("verifier completed before the test released it")
	default:
	}

	close(verifyRelease)
	verifierReleased = true
	select {
	case err := <-runDone:
		suite.Require().NoError(err)
	case <-ctx.Done():
		suite.FailNow("install did not finish before timeout")
	}

	var events []string
	progressOutput := strings.TrimSpace(progress.String())
	suite.Require().NotEmpty(progressOutput, "expected progress events")
	for _, line := range strings.Split(progressOutput, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var envelope jsonschema.Envelope
		suite.Require().NoError(
			json.Unmarshal([]byte(line), &envelope),
			"invalid progress line: %q in %q", line, progressOutput,
		)
		if envelope.Kind != "install.progress" {
			continue
		}
		var event jsonschema.InstallProgressEvent
		suite.Require().NoError(json.Unmarshal(envelope.Payload, &event))
		events = append(events, event.Event)
	}
	startIndex := -1
	completeIndex := -1
	for i, event := range events {
		if event == "verify.start" {
			startIndex = i
		}
		if event == "verify.complete" {
			completeIndex = i
		}
	}
	suite.Require().GreaterOrEqual(startIndex, 0, "expected verify.start progress event")
	suite.Require().GreaterOrEqual(completeIndex, 0, "expected verify.complete progress event")
	suite.Less(startIndex, completeIndex, "verification progress events should stay ordered")
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

type installStateObservedModel struct {
	model  progressiveInstallModel
	states chan installState
}

func (m *installStateObservedModel) Init() tea.Cmd {
	return m.model.Init()
}

func (m *installStateObservedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	previousState := m.model.state
	updated, cmd := m.model.Update(msg)
	m.model = updated.(progressiveInstallModel)
	if m.model.state != previousState {
		m.states <- m.model.state
	}
	return m, cmd
}

func (m *installStateObservedModel) View() tea.View {
	return m.model.View()
}
