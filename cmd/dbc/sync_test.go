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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/columnar-tech/dbc/internal/jsonschema"
)

func TestSyncProgressPercentTracksCompletedItems(t *testing.T) {
	if got := syncProgressPercent(0, 2); got != 0.5 {
		t.Fatalf("first completed item progress = %v, want 0.5", got)
	}
	if got := syncProgressPercent(1, 2); got != 1 {
		t.Fatalf("second completed item progress = %v, want 1", got)
	}
}

func TestAcquireSyncProjectLockDeadlineIsContentionButCancelIsNot(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), ".dbc.project.lock")
	held, err := fslock.Acquire(lockPath, time.Second)
	if err != nil {
		t.Fatalf("acquire holder lock: %v", err)
	}
	defer held.Release()

	deadlineCtx, cancelDeadline := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err = acquireSyncProjectLock(deadlineCtx, lockPath)
	cancelDeadline()
	if !errors.Is(err, fslock.ErrLockContended) {
		t.Fatalf("deadline while waiting for held project lock should be contention, got: %v", err)
	}
	if !strings.Contains(err.Error(), "another dbc operation is in progress") {
		t.Fatalf("expected contention-specific message, got: %v", err)
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = acquireSyncProjectLock(cancelCtx, lockPath)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("explicit cancellation should remain context.Canceled, got: %v", err)
	}
	if errors.Is(err, fslock.ErrLockContended) {
		t.Fatalf("explicit cancellation must not be classified as contention: %v", err)
	}
}

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

type syncProgramRun struct {
	program *tea.Program
	done    chan struct{}
	model   tea.Model
	err     error
	output  bytes.Buffer
}

func startSyncProgram(model syncModel) *syncProgramRun {
	return startSyncProgramWithContext(model, nil)
}

func startSyncProgramWithContext(model syncModel, ctx context.Context) *syncProgramRun {
	run := &syncProgramRun{done: make(chan struct{})}
	model.jsonOut = &run.output
	options := []tea.ProgramOption{tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithFilter(filterProgramMessage)}
	if ctx != nil {
		options = append(options, tea.WithContext(ctx))
	}
	run.program = tea.NewProgram(model, options...)
	go func() {
		run.model, run.err = run.program.Run()
		notifyProgramExited(model)
		run.program.Wait()
		close(run.done)
	}()
	return run
}

func waitSyncTestSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for sync worker test signal")
	}
}

func waitSyncProgram(t *testing.T, run *syncProgramRun) syncModel {
	t.Helper()
	select {
	case <-run.done:
	case <-time.After(5 * time.Second):
		run.program.Send(tea.InterruptMsg{})
		t.Fatal("timed out waiting for sync program to finish")
	}
	if run.err != nil {
		t.Fatalf("sync program returned an error: %v", run.err)
	}
	model, ok := run.model.(syncModel)
	if !ok {
		t.Fatalf("unexpected sync model result %T", run.model)
	}
	return model
}

func (suite *SubcommandTestSuite) TestSyncCancellationWaitsForWorkerCleanup() {
	for _, stage := range []string{"registry", "download", "prepare", "candidate-save", "install"} {
		suite.Run(stage, func() {
			tmp := suite.T().TempDir()
			path := filepath.Join(tmp, "dbc.toml")
			lockPath := filepath.Join(tmp, "dbc.lock")
			projectLockPath := filepath.Join(tmp, ".dbc.project.lock")
			suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0600))
			suite.Require().NoError(writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion}))
			oldLock, err := os.ReadFile(lockPath)
			suite.Require().NoError(err)

			entered := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			var archive *os.File
			model := SyncCmd{Path: path, NoVerify: true, Json: stage != "prepare", JsonStreamProgress: stage == "prepare"}.GetModelCustom(baseModel{
				getDriverRegistry: func() ([]dbc.Driver, error) {
					if stage == "registry" {
						close(entered)
						<-release
					}
					return getTestDriverRegistry()
				},
				downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
					archive, err = downloadTestPkg(pkg)
					if stage == "download" {
						close(entered)
						<-release
					}
					return archive, err
				},
			}).(syncModel)
			block := func(context.Context) error {
				close(entered)
				<-release
				return nil
			}
			switch stage {
			case "prepare":
				model.worker.hooks.duringPrepare = func(ctx context.Context, _ int, item installItem) error {
					archive = item.Archive
					return block(ctx)
				}
			case "candidate-save":
				model.worker.hooks.beforeCandidateSave = block
			case "install":
				model.worker.hooks.installPackage = func(ctx context.Context, _ config.Config, _ string, file *os.File, _ config.ExpectedPackageMetadata, _ config.InstallOptions) (config.Manifest, error) {
					archive = file
					close(entered)
					<-release
					return config.Manifest{}, ctx.Err()
				}
			}

			run := startSyncProgram(model)
			waitSyncTestSignal(suite.T(), entered)
			run.program.Send(tea.InterruptMsg{})
			waitSyncTestSignal(suite.T(), model.worker.ctx.Done())
			select {
			case <-run.done:
				suite.FailNow("program exited before the worker completed")
			default:
			}
			select {
			case <-model.worker.done:
				suite.FailNow("worker exited while its test hook was blocked")
			default:
			}
			var statErr error
			if archive != nil {
				_, statErr = archive.Stat()
				suite.NoError(statErr, "prepared archive must stay open until the worker exits")
			} else {
				suite.Equal("registry", stage, "only registry discovery runs before an archive is opened")
			}
			lockCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			_, lockErr := fslock.AcquireContext(lockCtx, projectLockPath)
			cancel()
			suite.ErrorIs(lockErr, context.DeadlineExceeded, "project lock must stay held until the worker exits")

			unblock()
			result := waitSyncProgram(suite.T(), run)
			suite.Equal(1, result.Status())
			suite.ErrorIs(result.Err(), context.Canceled)
			suite.Empty(result.FinalOutput())
			var lines []string
			for _, line := range strings.Split(strings.TrimSpace(run.output.String()), "\n") {
				if line != "" {
					lines = append(lines, line)
				}
			}
			errorEnvelopes := 0
			for _, line := range lines {
				var envelope jsonschema.Envelope
				suite.NoError(json.Unmarshal([]byte(line), &envelope), "each JSON progress line must be valid")
				if envelope.Kind != "error" {
					suite.NotEqual("sync.status", envelope.Kind, "cancel must not produce successful final output")
					continue
				}
				errorEnvelopes++
				var response jsonschema.ErrorResponse
				suite.NoError(json.Unmarshal(envelope.Payload, &response))
				suite.Equal("sync_failed", response.Code)
				suite.Equal(context.Canceled.Error(), response.Message)
			}
			suite.Equal(1, errorEnvelopes, "cancel should emit one terminal JSON error envelope")
			waitSyncTestSignal(suite.T(), model.worker.done)
			if archive != nil {
				_, statErr = archive.Stat()
				suite.ErrorIs(statErr, os.ErrClosed)
			}
			projectLock, lockErr := fslock.Acquire(projectLockPath, time.Second)
			suite.NoError(lockErr)
			if lockErr == nil {
				suite.NoError(projectLock.Release())
			}
			newLock, err := os.ReadFile(lockPath)
			suite.Require().NoError(err)
			if stage == "install" {
				suite.NotEqual(oldLock, newLock, "candidate lock remains after execution begins")
			} else {
				suite.Equal(oldLock, newLock, "cancellation before candidate save preserves old lock")
			}
			_, err = config.GetDriver(config.Config{Level: config.ConfigEnv, Location: suite.Dir()}, "test-driver-1")
			suite.Error(err, "test install hook must prevent runtime installation")
		})
	}
}

func (suite *SubcommandTestSuite) TestSyncProgramContextCancellationJoinsWorker() {
	tmp := suite.T().TempDir()
	path := filepath.Join(tmp, "dbc.toml")
	projectLockPath := filepath.Join(tmp, ".dbc.project.lock")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0600))
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var archive *os.File
	model := SyncCmd{Path: path, NoVerify: true, Json: true}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			var err error
			archive, err = downloadTestPkg(pkg)
			close(entered)
			<-release // Deliberately contextless, matching the downloader contract.
			return archive, err
		},
	}).(syncModel)
	programCtx, cancelProgram := context.WithCancel(context.Background())
	run := startSyncProgramWithContext(model, programCtx)
	waitSyncTestSignal(suite.T(), entered)
	cancelProgram()
	waitSyncTestSignal(suite.T(), model.worker.ctx.Done())
	select {
	case <-run.done:
		suite.FailNow("runner must join the worker before returning")
	default:
	}
	suite.NotNil(archive)
	_, err := archive.Stat()
	suite.NoError(err, "archive must remain owned until the blocked downloader returns")
	lockCtx, cancelLock := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, lockErr := fslock.AcquireContext(lockCtx, projectLockPath)
	cancelLock()
	suite.ErrorIs(lockErr, context.DeadlineExceeded, "project lock must remain held during cancellation cleanup")

	unblock()
	waitSyncTestSignal(suite.T(), run.done)
	waitSyncTestSignal(suite.T(), model.worker.done)
	suite.ErrorIs(run.err, context.Canceled)
	_, err = archive.Stat()
	suite.ErrorIs(err, os.ErrClosed)
	projectLock, err := fslock.Acquire(projectLockPath, time.Second)
	suite.NoError(err)
	if err == nil {
		suite.NoError(projectLock.Release())
	}
}

func (suite *SubcommandTestSuite) TestSyncCancellationWhileWaitingForProjectLock() {
	tmp := suite.T().TempDir()
	path := filepath.Join(tmp, "dbc.toml")
	projectLockPath := filepath.Join(tmp, ".dbc.project.lock")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0600))
	holder, err := fslock.Acquire(projectLockPath, time.Second)
	suite.Require().NoError(err)
	var holderReleaseOnce sync.Once
	releaseHolder := func() { holderReleaseOnce.Do(func() { _ = holder.Release() }) }
	defer releaseHolder()
	registryCalls, downloadCalls := atomic.Int32{}, atomic.Int32{}
	model := SyncCmd{Path: path, NoVerify: true, Json: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			registryCalls.Add(1)
			return getTestDriverRegistry()
		},
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloadCalls.Add(1)
			return downloadTestPkg(pkg)
		},
	}).(syncModel)
	run := startSyncProgram(model)
	deadline := time.After(2 * time.Second)
	for !model.worker.hasStarted() {
		select {
		case <-deadline:
			suite.FailNow("sync worker did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	time.Sleep(100 * time.Millisecond)
	run.program.Send(tea.InterruptMsg{})
	result := waitSyncProgram(suite.T(), run)
	suite.Equal(1, result.Status())
	suite.ErrorIs(result.Err(), context.Canceled)
	suite.Zero(registryCalls.Load(), "registry discovery must wait for project lock acquisition")
	suite.Zero(downloadCalls.Load())
	waitSyncTestSignal(suite.T(), model.worker.done)
	releaseHolder()
	projectLock, err := fslock.Acquire(projectLockPath, time.Second)
	suite.NoError(err, "canceled lock waiter must not retain the project lock")
	if err == nil {
		suite.NoError(projectLock.Release())
	}
}

func (suite *SubcommandTestSuite) TestSyncSerializesSameProjectUntilWorkerFinishes() {
	tmp := suite.T().TempDir()
	path := filepath.Join(tmp, "dbc.toml")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0600))
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var registryCalls atomic.Int32
	first := SyncCmd{Path: path, NoVerify: true, Json: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			registryCalls.Add(1)
			return getTestDriverRegistry()
		},
		downloadPkg: downloadTestPkg,
	}).(syncModel)
	first.worker.hooks.beforeCandidateSave = func(context.Context) error {
		close(entered)
		<-release
		return nil
	}
	firstRun := startSyncProgram(first)
	waitSyncTestSignal(suite.T(), entered)

	secondDownloads, secondInstalls := atomic.Int32{}, atomic.Int32{}
	second := SyncCmd{Path: path, NoVerify: true, Json: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			registryCalls.Add(1)
			return getTestDriverRegistry()
		},
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			secondDownloads.Add(1)
			return downloadTestPkg(pkg)
		},
	}).(syncModel)
	second.worker.hooks.installPackage = func(_ context.Context, cfg config.Config, driver string, archive *os.File, expected config.ExpectedPackageMetadata, options config.InstallOptions) (config.Manifest, error) {
		secondInstalls.Add(1)
		return config.InstallPackage(cfg, driver, archive, expected, options)
	}
	secondRun := startSyncProgram(second)
	deadline := time.After(2 * time.Second)
	for !second.worker.hasStarted() {
		select {
		case <-deadline:
			suite.FailNow("second sync did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	time.Sleep(150 * time.Millisecond)
	suite.Equal(int32(1), registryCalls.Load(), "second sync must wait before registry discovery")
	select {
	case <-secondRun.done:
		suite.FailNow("second sync exited before acquiring the project lock")
	default:
	}

	unblock()
	firstResult := waitSyncProgram(suite.T(), firstRun)
	secondResult := waitSyncProgram(suite.T(), secondRun)
	suite.Equal(0, firstResult.Status())
	suite.Equal(0, secondResult.Status())
	suite.Equal(int32(1), registryCalls.Load(), "candidate lock should let the second sync avoid discovery")
	suite.Len(secondResult.skippedDrivers, 1, "runtime state must be reloaded after the second sync acquires the lock")
	suite.Empty(secondResult.newlyInstalled)
	suite.Zero(secondDownloads.Load(), "the second sync should reuse the installed exact artifact")
	suite.Zero(secondInstalls.Load(), "the second sync should skip the already-installed driver")
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

type syncInjectedMessageModel struct {
	model   syncModel
	message tea.Msg
	result  *syncModel
}

func (m syncInjectedMessageModel) Init() tea.Cmd {
	return func() tea.Msg { return m.message }
}

func (m syncInjectedMessageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.model.Update(msg)
	if next, ok := updated.(syncModel); ok {
		m.model = next
		if m.result != nil {
			*m.result = next
		}
		return m, cmd
	}
	return updated, cmd
}

func (m syncInjectedMessageModel) View() tea.View {
	return m.model.View()
}

func (m syncInjectedMessageModel) WithJSONWriter(w io.Writer) tea.Model {
	m.model.jsonOut = w
	return m
}

func (m syncInjectedMessageModel) Status() int { return m.model.Status() }
func (m syncInjectedMessageModel) Err() error  { return m.model.Err() }
func (m syncInjectedMessageModel) FinalOutput() string {
	return m.model.FinalOutput()
}
func (m syncInjectedMessageModel) IsJSONMode() bool { return m.model.IsJSONMode() }

func (suite *SubcommandTestSuite) TestSyncTerminalErrorsUseSingleOutputContract() {
	for _, failure := range []string{"generic", "checksum-read", "checksum-mismatch"} {
		for _, mode := range []string{"plain", "json", "json-stream"} {
			suite.Run(failure+"/"+mode, func() {
				var (
					message tea.Msg
					wantErr error
					code    string
				)
				switch failure {
				case "generic":
					wantErr = errors.New("injected sync failure")
					message = wantErr
					code = "sync_failed"
				case "checksum-read":
					missingPath := filepath.Join(suite.T().TempDir(), "missing-driver.so")
					_, wantErr = checksum(missingPath)
					info := config.DriverInfo{ID: "example", Version: semver.MustParse("1.0.0")}
					info.Driver.Shared.Set(config.PlatformTuple(), missingPath)
					message = installedDrvMsg{info: info}
					code = "checksum_failed"
				case "checksum-mismatch":
					libraryPath := filepath.Join(suite.T().TempDir(), "driver.so")
					suite.Require().NoError(os.WriteFile(libraryPath, []byte("installed library"), 0600))
					info := config.DriverInfo{ID: "example", Version: semver.MustParse("1.0.0")}
					info.Driver.Shared.Set(config.PlatformTuple(), libraryPath)
					item := installItem{InstalledLibraryHash: strings.Repeat("0", 64)}
					message = installedDrvMsg{info: info, item: item}
					wantErr = errors.New("installed library checksum does not match validated package")
					code = "checksum_failed"
				}

				jsonOutput := mode != "plain"
				var result syncModel
				model := syncInjectedMessageModel{
					model: syncModel{
						jsonOutput:         jsonOutput,
						jsonStreamProgress: mode == "json-stream",
					},
					message: message,
					result:  &result,
				}
				output := suite.runCmdErr(model)
				suite.Equal(1, result.Status())
				suite.EqualError(result.Err(), wantErr.Error())
				suite.Empty(result.FinalOutput(), "failure must not produce a success sync.status envelope")

				if !jsonOutput {
					suite.Equal("\nError: "+wantErr.Error(), output)
					suite.Equal(1, strings.Count(output, wantErr.Error()), "terminal error should be reported once")
					return
				}

				suite.NotContains(output, "Error:", "JSON output must not contain plaintext error formatting")
				lines := strings.Split(strings.TrimSpace(output), "\n")
				suite.Len(lines, 1, "failure should produce exactly one terminal JSON envelope")
				var envelope jsonschema.Envelope
				suite.NoError(json.Unmarshal([]byte(lines[0]), &envelope))
				suite.Equal("error", envelope.Kind)
				var response jsonschema.ErrorResponse
				suite.NoError(json.Unmarshal(envelope.Payload, &response))
				suite.Equal(code, response.Code)
				suite.Equal(wantErr.Error(), response.Message)
			})
		}
	}
}
