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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/columnar-tech/dbc/internal/packslip"
	"github.com/columnar-tech/dbc/internal/resolution"
)

type installPackslipResolverStub struct {
	release resolution.ResolvedRelease
	calls   int
	project string
	request packslip.Request
}

func (stub *installPackslipResolverStub) Resolve(_ context.Context, source packslip.PackslipSource, request packslip.Request) (resolution.ResolvedRelease, error) {
	stub.calls++
	stub.project = source.Project
	stub.request = request
	return cloneResolvedReleaseForSync(stub.release), nil
}

func packslipInstallRelease(t *testing.T, id, version, artifactURL string, archive []byte) resolution.ResolvedRelease {
	t.Helper()
	target, err := resolution.TargetFromPlatformTuple(config.PlatformTuple())
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	size := int64(len(archive))
	return resolution.ResolvedRelease{
		DriverID: id,
		Version:  version,
		Source:   resolution.SourceSpec{Type: "packslip", Reference: "github.com/example/repo"},
		Evidence: []resolution.Evidence{{
			Kind: resolution.EvidenceKindReleaseMetadata,
			Location: resolution.ArtifactLocation{
				Kind: resolution.ArtifactLocationURL, Value: "https://github.com/example/repo/releases/download/v1.2.3/packslip.sigstore.json",
			},
			Hash: "sha256:" + strings.Repeat("a", 64),
		}},
		Artifacts: []resolution.Artifact{{
			Target: target, Format: "tgz", PackageVersion: 2,
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: artifactURL},
			Hash:     "sha256:" + hex.EncodeToString(digest[:]), Size: &size,
		}},
	}
}

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

	m = InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(testBaseModel())
	suite.validateOutput("\r[✓] searching\r\n[✓] downloading\r\n[✓] installing\r\n[✓] verifying signature\r\n",
		"\nRemoved conflicting driver: test-driver-1 (version: 1.0.0)\nInstalled test-driver-1 1.1.0 to "+suite.Dir(),
		suite.runCmd(m))

	installed, err := config.GetDriver(config.Config{Level: suite.configLevel, Location: suite.Dir()}, "test-driver-1")
	suite.Require().NoError(err)
	libraryPath := installed.Driver.Shared.Get(config.PlatformTuple())
	relLibrary, err := filepath.Rel(suite.Dir(), libraryPath)
	suite.Require().NoError(err)
	relDir := filepath.ToSlash(filepath.Dir(relLibrary))
	relLibrary = filepath.ToSlash(relLibrary)
	suite.Equal([]string{filepath.ToSlash(filepath.Join(relDir, "dbc-install-receipt.json")),
		relLibrary, relLibrary + ".sig", "test-driver-1.toml"}, suite.getFilesInDir(suite.Dir()))
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

func (suite *SubcommandTestSuite) TestInstallPackslipDirectUsesSignedIDWithoutRegistryLookup() {
	archive := packageV2ArchiveForInstall(suite.T(), "signed-driver-id", "1.2.3", config.PlatformTuple())
	artifactURL := "https://assets.example.test/releases/package.tgz?sig=preserve-me"
	resolver := &installPackslipResolverStub{release: packslipInstallRelease(suite.T(), "signed-driver-id", "1.2.3", artifactURL, archive)}
	base := testBaseModel()
	registryCalls, artifactCalls := 0, 0
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		registryCalls++
		return nil, errors.New("registry lookup must not be used for Packslip")
	}
	base.newPackslipResolver = func() (packslip.Resolver, error) { return resolver, nil }
	base.fetchPackslipArtifact = func(_ context.Context, resolvedURL *url.URL) (io.ReadCloser, error) {
		artifactCalls++
		suite.Equal("sig=preserve-me", resolvedURL.RawQuery)
		return io.NopCloser(bytes.NewReader(archive)), nil
	}
	projectDir := suite.T().TempDir()
	originalDir, err := os.Getwd()
	suite.Require().NoError(err)
	suite.Require().NoError(os.Chdir(projectDir))
	suite.T().Cleanup(func() { suite.Require().NoError(os.Chdir(originalDir)) })

	model := InstallCmd{Driver: "github.com/example/repo=1.2.3", Level: suite.configLevel, Json: true}.
		GetModelCustom(base)
	out := suite.runCmd(model)
	suite.Equal(0, registryCalls)
	suite.Equal(1, resolver.calls)
	suite.Equal("github.com/example/repo", resolver.project)
	suite.Empty(resolver.request.DriverID, "direct install must adopt the signed driver ID")
	suite.Equal("1.2.3", resolver.request.Version)
	suite.Equal(1, artifactCalls)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	var envelope jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(lines[len(lines)-1]), &envelope))
	suite.Equal("install.status", envelope.Kind)
	var status jsonschema.InstallStatus
	suite.Require().NoError(json.Unmarshal(envelope.Payload, &status))
	suite.Equal("signed-driver-id", status.Driver)
	suite.Equal("1.2.3", status.Version)
	suite.Equal(&jsonschema.InstallSource{Type: "packslip", Reference: "github.com/example/repo"}, status.Source)
	suite.driverIsInstalled("signed-driver-id", true)
	for _, path := range []string{"dbc.toml", "dbc.lock", ".dbc.project.lock"} {
		_, statErr := os.Stat(filepath.Join(projectDir, path))
		suite.True(os.IsNotExist(statErr), "%s should not be created by direct install", path)
	}
}

func (suite *SubcommandTestSuite) TestInstallPackslipDirectFailsClosedBeforeRuntimeMutation() {
	tests := []struct {
		name          string
		packageID     string
		badHash       bool
		hostReq       bool
		wantError     string
		wantFetchCall int
	}{
		{name: "package ID mismatch", packageID: "different-id", wantError: "package id mismatch", wantFetchCall: 1},
		{name: "signed archive hash mismatch despite skip flags", packageID: "bad-hash-id", badHash: true, wantError: "does not match expected hash", wantFetchCall: 1},
		{name: "unsupported host requirements", packageID: "host-req-id", hostReq: true, wantError: "unsupported host requirements", wantFetchCall: 0},
	}
	for _, tc := range tests {
		suite.Run(tc.name, func() {
			archiveID := tc.packageID
			if tc.name == "package ID mismatch" {
				archiveID = "archive-id"
			}
			archive := packageV2ArchiveForInstall(suite.T(), archiveID, "1.2.3", config.PlatformTuple())
			artifactURL := "https://assets.example.test/releases/" + tc.packageID + ".tgz"
			resolver := &installPackslipResolverStub{release: packslipInstallRelease(suite.T(), tc.packageID, "1.2.3", artifactURL, archive)}
			if tc.badHash {
				resolver.release.Artifacts[0].Hash = "sha256:" + strings.Repeat("0", 64)
			}
			if tc.hostReq {
				resolver.release.Artifacts[0].HostRequirements.OSMin = "99"
			}
			base := testBaseModel()
			registryCalls, fetchCalls := 0, 0
			base.getDriverRegistry = func() ([]dbc.Driver, error) {
				registryCalls++
				return nil, errors.New("registry lookup must not be used")
			}
			base.newPackslipResolver = func() (packslip.Resolver, error) { return resolver, nil }
			base.fetchPackslipArtifact = func(context.Context, *url.URL) (io.ReadCloser, error) {
				fetchCalls++
				return io.NopCloser(bytes.NewReader(archive)), nil
			}
			command := InstallCmd{
				Driver: "github.com/example/repo=1.2.3", Level: suite.configLevel,
				NoVerify: true, InsecureNoChecksum: true,
			}.GetModelCustom(base)
			out := suite.runCmdErr(command)
			suite.Contains(out, tc.wantError)
			suite.Equal(0, registryCalls)
			suite.Equal(tc.wantFetchCall, fetchCalls)
			_, err := config.GetDriver(config.Config{Level: suite.configLevel, Location: suite.Dir()}, tc.packageID)
			suite.Error(err, "failed Packslip proof must not create a runtime registration")
		})
	}
}

func (suite *SubcommandTestSuite) TestInstallPackslipRejectsInvalidInputsAndPreBeforeResolver() {
	for _, input := range []string{
		"github.com/example/repo>=1.2.3",
		"github.com/example/repo=latest",
		"github.com/example/repo=v1.2.3",
		"https://github.com/example/repo/releases/download/v1.2.3/package.tgz",
	} {
		suite.Run(input, func() {
			resolverCalls, registryCalls := 0, 0
			base := testBaseModel()
			base.newPackslipResolver = func() (packslip.Resolver, error) {
				resolverCalls++
				return nil, errors.New("resolver must not be created")
			}
			base.getDriverRegistry = func() ([]dbc.Driver, error) {
				registryCalls++
				return nil, errors.New("registry must not be queried")
			}
			out := suite.runCmdErr(InstallCmd{Driver: input, Level: suite.configLevel}.GetModelCustom(base))
			suite.Contains(out, "Packslip")
			suite.Zero(resolverCalls)
			suite.Zero(registryCalls)
		})
	}
	resolverCalls, registryCalls := 0, 0
	base := testBaseModel()
	base.newPackslipResolver = func() (packslip.Resolver, error) {
		resolverCalls++
		return nil, errors.New("resolver must not be created")
	}
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		registryCalls++
		return nil, errors.New("registry must not be queried")
	}
	out := suite.runCmdErr(InstallCmd{Driver: "github.com/example/repo=1.2.3", Level: suite.configLevel, Pre: true}.GetModelCustom(base))
	suite.Contains(out, "--pre does not apply")
	suite.Zero(resolverCalls)
	suite.Zero(registryCalls)
}

func (suite *SubcommandTestSuite) TestInstallLocalPackageV2UsesMetadataIDAndDoesNotReadProjectFiles() {
	archive := packageV2ArchiveForInstall(suite.T(), "declared-driver-id", "1.2.3", config.PlatformTuple())
	projectDir := suite.T().TempDir()
	originalDir, err := os.Getwd()
	suite.Require().NoError(err)
	suite.Require().NoError(os.Chdir(projectDir))
	suite.T().Cleanup(func() { suite.Require().NoError(os.Chdir(originalDir)) })
	suite.Require().NoError(os.WriteFile("misleading-name.tgz", archive, 0o600))
	registryCalls := 0
	base := testBaseModel()
	base.getDriverRegistry = func() ([]dbc.Driver, error) {
		registryCalls++
		return nil, errors.New("registry lookup must not be used for a local package")
	}
	out := suite.runCmd(InstallCmd{Driver: "misleading-name.tgz", Level: suite.configLevel, Json: true}.GetModelCustom(base))
	suite.Equal(0, registryCalls)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var envelope jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(lines[len(lines)-1]), &envelope))
	var status jsonschema.InstallStatus
	suite.Require().NoError(json.Unmarshal(envelope.Payload, &status))
	suite.Equal("declared-driver-id", status.Driver)
	suite.Equal(&jsonschema.InstallSource{Type: "path", Reference: "misleading-name.tgz"}, status.Source)
	suite.driverIsInstalled("declared-driver-id", true)
	for _, path := range []string{"dbc.toml", "dbc.lock", ".dbc.project.lock"} {
		_, statErr := os.Stat(filepath.Join(projectDir, path))
		suite.True(os.IsNotExist(statErr), "%s should not be created by direct install", path)
	}
}

func TestParsePackslipInstallArgumentUsesCanonicalExactVersion(t *testing.T) {
	project, version, matched, err := parsePackslipInstallArgument("github.com/owner/repo/tool=1.2.3-rc.1+build.5")
	if err != nil || !matched || project != "github.com/owner/repo/tool" || version != "1.2.3-rc.1+build.5" {
		t.Fatalf("valid Packslip direct argument parsed as %q, %q, %v, %v", project, version, matched, err)
	}
	for _, input := range []string{
		"github.com/owner/repo>=1.2.3",
		"github.com/owner/repo=1.2",
		"github.com/owner/repo=01.2.3",
		"github.com/owner/repo=v1.2.3",
		"github.com/owner/repo=latest",
	} {
		_, _, matched, err := parsePackslipInstallArgument(input)
		if !matched || err == nil {
			t.Errorf("invalid Packslip direct argument %q was not rejected before registry fallback", input)
		}
	}
	if _, _, matched, err := parsePackslipInstallArgument("owner/repo=1.2.3"); matched || err != nil {
		t.Fatalf("short owner/repo syntax should not be interpreted as Packslip: matched=%v err=%v", matched, err)
	}
}

func TestDirectLocalPackageRejectsReplacementAfterResolution(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "driver.tgz")
	first := packageV2ArchiveForInstall(t, "local-driver", "1.2.3", config.PlatformTuple())
	if err := os.WriteFile(path, first, 0o600); err != nil {
		t.Fatal(err)
	}
	model := progressiveInstallModel{
		isLocal:          true,
		localPackagePath: path,
		NoVerify:         true,
		cfg:              config.Config{Level: config.ConfigEnv, Location: filepath.Join(root, "install")},
	}
	item, err := model.resolveDirectInstall(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = makeSyncPackageV2Archive(t, path, "local-driver", "1.2.3", config.PlatformTuple())
	executor, err := model.newDirectInstallExecutor(item)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.prepareItem(context.Background(), &item); err == nil || !strings.Contains(err.Error(), "does not match expected hash") {
		t.Fatalf("replacement archive error = %v, want expected-hash mismatch", err)
	}
	if item.Archive != nil {
		if err := item.Archive.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: filepath.Join(root, "install")}, "local-driver"); err == nil {
		t.Fatal("replacement archive should not mutate the runtime installation")
	}
}

func TestInstallConfigForEnsurePreservesPrimaryWithMissingSecondary(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	secondary := filepath.Join(root, "secondary")
	missing := filepath.Join(root, "missing")
	if err := os.MkdirAll(secondary, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := installConfigForEnsure(config.Config{
		Level:    config.ConfigEnv,
		Location: strings.Join([]string{primary, secondary, missing}, string(filepath.ListSeparator)),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{primary, secondary}, string(filepath.ListSeparator))
	if cfg.Location != want {
		t.Fatalf("ensure install roots = %q, want %q", cfg.Location, want)
	}
	if info, err := os.Stat(primary); err != nil || !info.IsDir() {
		t.Fatalf("primary install root was not created: info=%v err=%v", info, err)
	}
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
	}{{"dbc-package.toml", []byte(manifest)}, {"driver.so", []byte("driver library")}} {
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
	t.Cleanup(func() { _ = f.Close() })
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	return f
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
	suite.Equal(&jsonschema.InstallSource{Type: "registry", Reference: testRegistry.BaseURL.String()}, status.Source)
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

// TestInstallJSON_AlreadyInstalledLibraryTamperingIsRepaired verifies that
// receipt/library integrity changes force repair instead of version-only skip.
func (suite *SubcommandTestSuite) TestInstallJSON_AlreadyInstalledLibraryTamperingIsRepaired() {
	// First install the driver normally.
	m := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	suite.runCmd(m)

	// Locate and delete the shared library to simulate runtime tampering.
	cfg := config.Get()[suite.configLevel]
	driver, err := config.GetDriver(cfg, "test-driver-1")
	suite.Require().NoError(err)
	sharedPath := driver.Driver.Shared.Get(config.PlatformTuple())
	suite.Require().NotEmpty(sharedPath, "shared library path should not be empty")
	suite.Require().NoError(os.Remove(sharedPath))

	// Reinstall with --json. The package executor must notice that the current
	// library no longer matches its receipt and repair it from the registry.
	m2 := InstallCmd{Driver: "test-driver-1", Level: suite.configLevel, Json: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m2)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var env jsonschema.Envelope
	suite.Require().NoError(json.Unmarshal([]byte(lines[len(lines)-1]), &env))
	suite.Equal("install.status", env.Kind)
	var status jsonschema.InstallStatus
	suite.Require().NoError(json.Unmarshal(env.Payload, &status))
	suite.Equal("installed", status.Status)
	repaired, err := config.GetDriver(config.Get()[suite.configLevel], "test-driver-1")
	suite.Require().NoError(err)
	suite.FileExists(repaired.Driver.Shared.Get(config.PlatformTuple()))
}
