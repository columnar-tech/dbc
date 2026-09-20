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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

func (suite *SubcommandTestSuite) TestSync() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0 already installed\r\n\rDone!\r\n", "", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSyncWithVersion() {
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
			m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
			suite.runCmd(m)

			m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{tt.driver}}.GetModel()
			suite.runCmd(m)

			m = SyncCmd{
				Path: filepath.Join(suite.tempdir, "dbc.toml"),
			}.GetModelCustom(
				testBaseModel())
			suite.validateOutput("✓ test-driver-1-"+tt.expectedVersion+"\r\n\rDone!\r\n", "", suite.runCmd(m))
			suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
			suite.FileExists(filepath.Join(suite.tempdir, "dbc.lock"))

			for _, f := range suite.getFilesInTempDir() {
				os.Remove(filepath.Join(suite.tempdir, f))
			}
		})
	}
}

func (suite *SubcommandTestSuite) TestSyncVirtualEnv() {
	suite.T().Setenv("ADBC_DRIVER_PATH", "")

	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	suite.T().Setenv("VIRTUAL_ENV", suite.tempdir)

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "etc", "adbc", "drivers", "test-driver-1.toml"))

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0 already installed\r\n\rDone!\r\n", "", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSyncCondaPrefix() {
	suite.T().Setenv("ADBC_DRIVER_PATH", "")

	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	suite.T().Setenv("CONDA_PREFIX", suite.tempdir)

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "etc", "adbc", "drivers", "test-driver-1.toml"))

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0 already installed\r\n\rDone!\r\n", "", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSyncInstallFailSig() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-no-sig"}}.GetModel()
	suite.runCmd(m)

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		testBaseModel())
	suite.validateOutput("\r ",
		"\nError: failed to verify signature: signature file 'test-driver-1-not-valid.so.sig' for driver is missing",
		suite.runCmdErr(m))
	suite.Equal([]string{"dbc.toml"}, suite.getFilesInTempDir())
}

func (suite *SubcommandTestSuite) TestSyncSignatureFailurePreservesExistingInstallation() {
	oldArchive, err := os.Open(filepath.Join("testdata", "test-driver-1.tar.gz"))
	suite.Require().NoError(err)
	cfg := config.Config{Level: config.ConfigEnv, Location: suite.Dir()}
	oldManifest, err := config.InstallPackage(cfg, "test-driver-no-sig", oldArchive, config.ExpectedPackageMetadata{
		ID: "test-driver-no-sig", Version: "1.0.0", Platform: config.PlatformTuple(),
		SourceType: "registry", SourceIdentity: testRegistry.BaseURL.String(),
	}, config.InstallOptions{
		Verify: func(stagingDir string, manifest config.Manifest) error {
			return dbc.VerifyPackageSignature(stagingDir, manifest)
		},
	})
	suite.Require().NoError(err)
	oldManifest.Version = semver.MustParse("0.9.0")
	suite.Require().NoError(config.CreateManifest(cfg, oldManifest.DriverInfo))
	oldManifestBytes, err := os.ReadFile(filepath.Join(suite.Dir(), "test-driver-no-sig.toml"))
	suite.Require().NoError(err)
	oldLibraryPath := oldManifest.Driver.Shared.Get(config.PlatformTuple())
	oldLibraryBytes, err := os.ReadFile(oldLibraryPath)
	suite.Require().NoError(err)

	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)
	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-no-sig"}}.GetModel()
	suite.runCmd(m)
	m = SyncCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModelCustom(testBaseModel())
	out := suite.runCmdErr(m)
	suite.Contains(out, "signature file 'test-driver-1-not-valid.so.sig' for driver is missing")

	manifestBytes, err := os.ReadFile(filepath.Join(suite.Dir(), "test-driver-no-sig.toml"))
	suite.Require().NoError(err)
	suite.Equal(oldManifestBytes, manifestBytes)
	libraryBytes, err := os.ReadFile(oldLibraryPath)
	suite.Require().NoError(err)
	suite.Equal(oldLibraryBytes, libraryBytes)
	installed, err := config.GetDriver(cfg, "test-driver-no-sig")
	suite.Require().NoError(err)
	suite.Equal("0.9.0", installed.Version.String())
}

func (suite *SubcommandTestSuite) TestSyncInstallNoVerify() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-no-sig"}}.GetModel()
	suite.runCmd(m)

	m = SyncCmd{
		Path:     filepath.Join(suite.tempdir, "dbc.toml"),
		NoVerify: true,
	}.GetModelCustom(
		testBaseModel())
	suite.validateOutput("✓ test-driver-no-sig-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
}

func (suite *SubcommandTestSuite) TestSyncPartialRegistryFailure() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	// Test that sync command handles partial registry failure gracefully
	// (one registry succeeds, another fails - returns both drivers and error)
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://backup-registry.example.com: failed to fetch driver registry: network timeout")
	}

	// Should succeed if the requested driver is found in the available drivers
	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg})

	// Should install successfully without printing the registry error
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
}

func (suite *SubcommandTestSuite) TestSyncPartialRegistryFailureDriverNotFound() {
	// Initialize driver list with a driver that doesn't exist
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	// Manually create a driver list with a nonexistent driver
	err := os.WriteFile(filepath.Join(suite.tempdir, "dbc.toml"), []byte(`# dbc driver list
[drivers]
[drivers.nonexistent-driver]
`), 0644)
	suite.Require().NoError(err)

	// Test that sync command shows registry errors when the requested driver is not found
	partialFailingRegistry := func() ([]dbc.Driver, error) {
		// Get drivers from the test registry (simulating one successful registry)
		drivers, _ := getTestDriverRegistry()
		// But also return an error (simulating another registry that failed)
		return drivers, fmt.Errorf("registry https://backup-registry.example.com: failed to fetch driver registry: network timeout")
	}

	// Should fail with enhanced error message if the requested driver is not found
	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: partialFailingRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmdErr(m)
	// Should show the driver not found error AND the registry error
	suite.Contains(out, "driver `nonexistent-driver` not found")
	suite.Contains(out, "Note: Some driver registries were unavailable")
	suite.Contains(out, "failed to fetch driver registry")
	suite.Contains(out, "network timeout")
}

func (suite *SubcommandTestSuite) TestSyncWithProjectRegistries() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	err := os.WriteFile(filepath.Join(suite.tempdir, "dbc.toml"), []byte(`# dbc driver list
[[registries]]
url = 'https://custom-registry.example.com'
name = 'custom'

[drivers]
[drivers.test-driver-1]
`), 0644)
	suite.Require().NoError(err)

	m = SyncCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModelCustom(testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))

	if os.Getenv("DBC_BASE_URL") == "" {
		suite.Require().NotNil(dbcClient)
		found := false
		for _, r := range dbcClient.Registries() {
			if r.BaseURL != nil && r.BaseURL.String() == "https://custom-registry.example.com" {
				found = true
				break
			}
		}
		suite.True(found, "expected custom registry in active client registries after sync with [[registries]] in dbc.toml")
	}
}

func (suite *SubcommandTestSuite) TestSyncWithProjectRegistriesBackwardCompat() {
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	m = SyncCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModelCustom(testBaseModel())
	suite.validateOutput("✓ test-driver-1-1.1.0\r\n\rDone!\r\n", "", suite.runCmd(m))
	suite.FileExists(filepath.Join(suite.tempdir, "test-driver-1.toml"))
}

func (suite *SubcommandTestSuite) TestSyncCompleteRegistryFailure() {
	// Initialize driver list
	m := InitCmd{Path: filepath.Join(suite.tempdir, "dbc.toml")}.GetModel()
	suite.runCmd(m)

	m = AddCmd{Path: filepath.Join(suite.tempdir, "dbc.toml"), Driver: []string{"test-driver-1"}}.GetModel()
	suite.runCmd(m)

	// Test that sync command handles complete registry failure (no drivers returned)
	completeFailingRegistry := func() ([]dbc.Driver, error) {
		return nil, fmt.Errorf("registry https://primary-registry.example.com: connection refused")
	}

	m = SyncCmd{
		Path: filepath.Join(suite.tempdir, "dbc.toml"),
	}.GetModelCustom(
		baseModel{getDriverRegistry: completeFailingRegistry, downloadPkg: downloadTestPkg})

	out := suite.runCmdErr(m)
	suite.Contains(out, "connection refused")
}

func (suite *SubcommandTestSuite) TestSync_JSONStream() {
	tmpDir := suite.T().TempDir()
	driverListPath := filepath.Join(tmpDir, "dbc.toml")
	err := os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0644)
	suite.Require().NoError(err)

	m := SyncCmd{Path: driverListPath, Json: true}.
		GetModelCustom(baseModel{getDriverRegistry: getTestDriverRegistry, downloadPkg: downloadTestPkg})
	out := suite.runCmd(m)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	suite.Greater(len(lines), 0, "expected at least one NDJSON line")

	var lastEnv jsonschema.Envelope
	for _, line := range lines {
		if line == "" {
			continue
		}
		var env jsonschema.Envelope
		suite.Require().NoError(json.Unmarshal([]byte(line), &env), "line must be valid JSON: %s", line)
		suite.Equal(1, env.SchemaVersion)
		lastEnv = env
	}
	suite.Equal("sync.status", lastEnv.Kind)

	var status jsonschema.SyncStatus
	suite.Require().NoError(json.Unmarshal(lastEnv.Payload, &status))
	suite.Len(status.Installed, 1)
	suite.Equal("test-driver-1", status.Installed[0].Name)
}

func (suite *SubcommandTestSuite) TestSync_JSONProgressStream() {
	tmpDir := suite.T().TempDir()
	driverListPath := filepath.Join(tmpDir, "dbc.toml")
	err := os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0644)
	suite.Require().NoError(err)

	m := SyncCmd{Path: driverListPath, JsonStreamProgress: true}.
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

	suite.Contains(kinds, "sync.progress")
	suite.Equal("sync.status", kinds[len(kinds)-1])
}

func (suite *SubcommandTestSuite) TestSyncPrepareFailurePreservesRuntimeAndLock() {
	path := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")
	err := os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n[drivers.test-driver-manifest-only]\n"), 0644)
	suite.Require().NoError(err)

	// Keep a prior runtime installation and lock snapshot to verify that a
	// later driver's prepare failure does not partially apply the sync.
	cfg := config.Config{Level: config.ConfigEnv, Location: suite.Dir()}
	archive, err := os.Open(filepath.Join("testdata", "test-driver-1.tar.gz"))
	suite.Require().NoError(err)
	_, err = config.InstallPackage(cfg, "test-driver-1", archive, config.ExpectedPackageMetadata{
		ID: "test-driver-1", Version: "1.0.0", Platform: config.PlatformTuple(),
		SourceType: "registry", SourceIdentity: testRegistry.BaseURL.String(),
	}, config.InstallOptions{})
	suite.Require().NoError(err)
	suite.NoError(archive.Close())
	suite.Require().NoError(writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion}))
	oldLock, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)

	badArchivePath := filepath.Join(suite.tempdir, "bad-package.tar.gz")
	suite.Require().NoError(os.WriteFile(badArchivePath, []byte("not a package archive"), 0600))
	downloaded := map[string]int{}
	downloadCalls := 0
	var firstArchive *os.File
	model := SyncCmd{Path: path, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloaded[pkg.Driver.Path]++
			downloadCalls++
			if downloadCalls == 2 {
				return os.Open(badArchivePath)
			}
			archive, err := downloadTestPkg(pkg)
			firstArchive = archive
			return archive, err
		},
	})
	suite.runCmdErr(model)

	installed, err := config.GetDriver(cfg, "test-driver-1")
	suite.Require().NoError(err)
	suite.Equal("1.0.0", installed.Version.String())
	_, err = config.GetDriver(cfg, "test-driver-manifest-only")
	suite.Error(err)
	newLock, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(oldLock, newLock)
	suite.Equal(2, len(downloaded))
	suite.Equal(2, downloadCalls)
	_, err = firstArchive.Stat()
	suite.ErrorIs(err, os.ErrClosed)
}

func (suite *SubcommandTestSuite) TestSyncCandidateLockFailureDoesNotInstall() {
	path := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0644))
	suite.Require().NoError(writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion}))
	oldLock, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	downloadCount := 0
	var downloadedArchive *os.File
	model := SyncCmd{Path: path, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloadCount++
			downloadedArchive, err = downloadTestPkg(pkg)
			return downloadedArchive, err
		},
	}).(syncModel)
	model.writeCandidateLock = func(string, LockFile) error { return fmt.Errorf("injected lock failure") }
	suite.runCmdErr(model)
	suite.Equal(1, downloadCount)
	_, err = config.GetDriver(config.Config{Level: config.ConfigEnv, Location: suite.Dir()}, "test-driver-1")
	suite.Error(err)
	newLock, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(oldLock, newLock)
	_, err = downloadedArchive.Stat()
	suite.ErrorIs(err, os.ErrClosed)
}

func (suite *SubcommandTestSuite) TestSyncInstallFailureKeepsCompleteCandidateLock() {
	path := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n[drivers.test-driver-no-sig]\n"), 0644))
	var downloadedPaths []string
	var downloadedDrivers []string
	model := SyncCmd{Path: path, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			var source string
			switch pkg.Driver.Path {
			case "test-driver-1":
				source = filepath.Join("testdata", "test-driver-1.1.tar.gz")
			case "test-driver-no-sig":
				source = filepath.Join("testdata", "test-driver-no-sig.tar.gz")
			default:
				return nil, fmt.Errorf("unexpected driver %q", pkg.Driver.Path)
			}
			copyPath := suite.copyArchiveForSyncTest(source)
			downloadedPaths = append(downloadedPaths, copyPath)
			downloadedDrivers = append(downloadedDrivers, pkg.Driver.Path)
			return os.Open(copyPath)
		},
	}).(syncModel)
	model.writeCandidateLock = func(path string, lock LockFile) error {
		if err := writeLockFileAtomic(path, lock); err != nil {
			return err
		}
		if len(downloadedPaths) != 2 {
			return fmt.Errorf("expected two prepared archives, got %d", len(downloadedPaths))
		}
		// Corrupt the final item only after the complete candidate is durable,
		// forcing execution to fail after one install while preserving the lock.
		return os.WriteFile(downloadedPaths[len(downloadedPaths)-1], []byte("broken after prepare"), 0600)
	}
	suite.runCmdErr(model)

	lock, err := loadLockFile(lockPath)
	suite.Require().NoError(err)
	suite.Len(lock.lockinfo, 2)
	installedCount := 0
	for _, name := range []string{"test-driver-1", "test-driver-no-sig"} {
		if _, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: suite.Dir()}, name); err == nil {
			installedCount++
		}
	}
	suite.Equal(1, installedCount)
	suite.Require().Len(downloadedDrivers, 2)
	failedDriver := downloadedDrivers[len(downloadedDrivers)-1]
	lockedCandidate, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	candidate, err := loadLockFile(lockPath)
	suite.Require().NoError(err)
	lockedEntry := candidate.lockinfo[failedDriver]
	lockedArtifact, err := selectLockedArtifact(lockedEntry, config.PlatformTuple(), false)
	suite.Require().NoError(err)

	registryCalls, downloadCalls := 0, 0
	var observedPackage dbc.PkgInfo
	convergenceModel := SyncCmd{Path: path, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			registryCalls++
			return nil, fmt.Errorf("registry discovery should not run after candidate lock creation")
		},
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloadCalls++
			observedPackage = pkg
			switch pkg.Driver.Path {
			case "test-driver-1":
				return os.Open(filepath.Join("testdata", "test-driver-1.1.tar.gz"))
			case "test-driver-no-sig":
				return os.Open(filepath.Join("testdata", "test-driver-no-sig.tar.gz"))
			default:
				return nil, fmt.Errorf("unexpected locked driver %q", pkg.Driver.Path)
			}
		},
	})
	suite.runCmd(convergenceModel)
	suite.Equal(0, registryCalls)
	suite.Equal(1, downloadCalls)
	suite.Equal(failedDriver, observedPackage.Driver.Path)
	suite.Equal(lockedArtifact.Location.Value, observedPackage.Path.String())
	suite.Equal(lockedArtifact.Hash, observedPackage.ArtifactHash)
	suite.Require().NotNil(observedPackage.ArtifactSize)
	suite.Equal(*lockedArtifact.Size, *observedPackage.ArtifactSize)
	for _, name := range []string{"test-driver-1", "test-driver-no-sig"} {
		_, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: suite.Dir()}, name)
		suite.NoError(err)
	}
	convergedLock, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(lockedCandidate, convergedLock)
}

func (suite *SubcommandTestSuite) TestSyncLegacyLibraryProofUsesValidatedArchiveBeforeInstall() {
	path := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0644))
	archivePath := filepath.Join("testdata", "test-driver-1.tar.gz")
	archive, err := os.Open(archivePath)
	suite.Require().NoError(err)
	expected := config.ExpectedPackageMetadata{
		ID: "test-driver-1", Version: "1.0.0", Platform: config.PlatformTuple(),
		SourceType: "registry", SourceIdentity: testRegistry.BaseURL.String(),
	}
	validation, err := config.ValidatePackage("test-driver-1", archive, expected, config.InstallOptions{})
	suite.Require().NoError(err)
	suite.NoError(archive.Close())
	libraryHash := strings.TrimPrefix(validation.VerifiedLibraryHash, "sha256:")
	legacyLock := fmt.Sprintf("version = 1\n\n[[drivers]]\nname = %q\nversion = %q\nplatform = %q\nchecksum = %q\n", "test-driver-1", "1.0.0", config.PlatformTuple(), libraryHash)
	suite.Require().NoError(os.WriteFile(lockPath, []byte(legacyLock), 0644))

	model := SyncCmd{Path: path, NoVerify: true}.GetModelCustom(testBaseModel())
	suite.runCmd(model)
	updated, err := loadLockFile(lockPath)
	suite.Require().NoError(err)
	entry := updated.lockinfo["test-driver-1"]
	suite.Require().NotNil(entry.Legacy)
	suite.Equal(libraryHash, entry.Legacy.LibraryHash)
}

func (suite *SubcommandTestSuite) TestSyncPartialRegistryDownloadsEachArchiveOnceAndRejectsV2WithoutMetadata() {
	path := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n[drivers.test-driver-no-sig]\n"), 0644))
	downloaded := map[string]int{}
	model := SyncCmd{Path: path, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloaded[pkg.Driver.Path]++
			return downloadTestPkg(pkg)
		},
	})
	suite.runCmd(model)
	suite.Equal(map[string]int{"test-driver-1": 1, "test-driver-no-sig": 1}, downloaded)

	// The registry fixture omits archive metadata. A v2 archive must therefore
	// be rejected after its one download rather than treating measured values as
	// source-provided metadata.
	v2Path := filepath.Join(suite.tempdir, "v2-without-registry-metadata.tar.gz")
	suite.writeV2MetadataArchive(v2Path)
	v2List := filepath.Join(suite.tempdir, "v2.toml")
	suite.Require().NoError(os.WriteFile(v2List, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0644))
	v2Downloads := 0
	v2Model := SyncCmd{Path: v2List, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(dbc.PkgInfo) (*os.File, error) {
			v2Downloads++
			return os.Open(v2Path)
		},
	})
	suite.Contains(suite.runCmdErr(v2Model), "registry package v2 requires archive hash and size metadata")
	suite.Equal(1, v2Downloads)
}

func (suite *SubcommandTestSuite) TestSyncExactLockedArtifactConvergesWithoutRegistryDiscovery() {
	path := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0644))
	suite.runCmd(SyncCmd{Path: path, NoVerify: true}.GetModelCustom(testBaseModel()))
	registryCalls, downloadCalls := 0, 0
	model := SyncCmd{Path: path, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			registryCalls++
			return nil, fmt.Errorf("registry discovery should not run")
		},
		downloadPkg: func(dbc.PkgInfo) (*os.File, error) {
			downloadCalls++
			return nil, fmt.Errorf("download should not run")
		},
	})
	suite.runCmd(model)
	suite.Equal(0, registryCalls)
	suite.Equal(0, downloadCalls)
}

func (suite *SubcommandTestSuite) copyArchiveForSyncTest(source string) string {
	suite.T().Helper()
	input, err := os.Open(source)
	suite.Require().NoError(err)
	defer input.Close()
	output, err := os.CreateTemp(suite.tempdir, "sync-archive-*.tar.gz")
	suite.Require().NoError(err)
	_, err = io.Copy(output, input)
	suite.Require().NoError(err)
	suite.Require().NoError(output.Close())
	return output.Name()
}

func (suite *SubcommandTestSuite) writeV2MetadataArchive(path string) {
	suite.T().Helper()
	metadata := []byte(fmt.Sprintf(`package_version = 2
id = "test-driver-1"
name = "Test Driver 1"
version = "1.1.0"
platform = %q

[Driver]
entrypoint = "AdbcDriverTestInit"

[Files]
driver = "driver.so"
`, config.PlatformTuple()))
	file, err := os.Create(path)
	suite.Require().NoError(err)
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	suite.Require().NoError(tarWriter.WriteHeader(&tar.Header{Name: "dbc-package.toml", Mode: 0600, Size: int64(len(metadata)), Typeflag: tar.TypeReg}))
	_, err = tarWriter.Write(metadata)
	suite.Require().NoError(err)
	suite.Require().NoError(tarWriter.Close())
	suite.Require().NoError(gzipWriter.Close())
	suite.Require().NoError(file.Close())
}

func TestSyncJSONPostInstallChecksumMismatchReportsStructuredError(t *testing.T) {
	libraryPath := filepath.Join(t.TempDir(), "driver.so")
	if err := os.WriteFile(libraryPath, []byte("installed library"), 0600); err != nil {
		t.Fatal(err)
	}
	info := config.DriverInfo{ID: "example", Version: semver.MustParse("1.0.0")}
	info.Driver.Shared.Set(config.PlatformTuple(), libraryPath)
	const message = "installed library checksum does not match validated package"
	var output bytes.Buffer
	model := syncModel{
		jsonOutput: true,
		jsonOut:    &output,
		installItems: []installItem{{
			Driver:               dbc.Driver{Path: "example"},
			InstalledLibraryHash: strings.Repeat("0", 64),
		}},
	}
	updated, _ := model.Update(installedDrvMsg{
		info: info,
		item: model.installItems[0],
	})

	status := updated.(HasStatus)
	if status.Status() != 1 {
		t.Fatalf("status = %d, want 1", status.Status())
	}
	if status.Err() == nil || status.Err().Error() != message {
		t.Fatalf("Err() = %v, want %q", status.Err(), message)
	}
	var envelope jsonschema.Envelope
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("output is not a valid JSON envelope: %v; output %q", err, output.String())
	}
	if envelope.Kind != "error" {
		t.Fatalf("envelope kind = %q, want error", envelope.Kind)
	}
	var response jsonschema.ErrorResponse
	if err := json.Unmarshal(envelope.Payload, &response); err != nil {
		t.Fatalf("could not decode error payload: %v", err)
	}
	if response.Code != "checksum_failed" {
		t.Fatalf("error code = %q, want checksum_failed", response.Code)
	}
	if response.Message != message {
		t.Fatalf("error message = %q, want %q", response.Message, message)
	}
}

type syncInjectedMessageModel struct {
	model   syncModel
	message tea.Msg
}

func (m syncInjectedMessageModel) Init() tea.Cmd {
	return func() tea.Msg { return m.message }
}

func (m syncInjectedMessageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m.model.Update(msg)
}

func (m syncInjectedMessageModel) View() tea.View {
	return m.model.View()
}

func (suite *SubcommandTestSuite) TestSyncPlainPostInstallChecksumMismatchUsesSingleStandardError() {
	libraryPath := filepath.Join(suite.tempdir, "driver.so")
	suite.Require().NoError(os.WriteFile(libraryPath, []byte("installed library"), 0600))
	info := config.DriverInfo{ID: "example", Version: semver.MustParse("1.0.0")}
	info.Driver.Shared.Set(config.PlatformTuple(), libraryPath)
	const message = "installed library checksum does not match validated package"
	model := syncInjectedMessageModel{
		model: syncModel{installItems: []installItem{{
			Driver:               dbc.Driver{Path: "example"},
			InstalledLibraryHash: strings.Repeat("0", 64),
		}}},
		message: installedDrvMsg{info: info, item: installItem{InstalledLibraryHash: strings.Repeat("0", 64)}},
	}
	output := suite.runCmdErr(model)
	suite.Equal("\nError: "+message, output)
	suite.Equal(1, strings.Count(output, message))
}
