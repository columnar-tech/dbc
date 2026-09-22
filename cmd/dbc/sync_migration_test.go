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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncMigrationNotificationIsStructuredAndDeferredForJSON(t *testing.T) {
	var out bytes.Buffer
	worker := newSyncWorker()
	model := syncModel{
		jsonOutput: true,
		jsonOut:    &out,
		worker:     worker,
	}
	updated, _ := model.Update(syncLockMigratedMsg{fromVersion: lockFileVersionV1, toVersion: lockFileVersion})
	got := updated.(syncModel)
	require.NotNil(t, got.migration)
	assert.Equal(t, &jsonschema.SyncMigration{FromVersion: 1, ToVersion: 2}, got.migration)
	assert.Empty(t, out.String(), "--json keeps migration in the final status envelope")

	var status jsonschema.SyncStatus
	var envelope jsonschema.Envelope
	require.NoError(t, json.Unmarshal([]byte(got.FinalOutput()), &envelope))
	require.NoError(t, json.Unmarshal(envelope.Payload, &status))
	require.NotNil(t, status.Migration)
	assert.Equal(t, 1, status.Migration.FromVersion)
	assert.Equal(t, 2, status.Migration.ToVersion)
}

func TestSyncMigrationNotificationStreamsOnlyInProgressMode(t *testing.T) {
	var out bytes.Buffer
	worker := newSyncWorker()
	model := syncModel{
		jsonOutput:         true,
		jsonStreamProgress: true,
		jsonOut:            &out,
		worker:             worker,
	}
	updated, _ := model.Update(syncLockMigratedMsg{fromVersion: lockFileVersionV1, toVersion: lockFileVersion})
	got := updated.(syncModel)
	var envelope jsonschema.Envelope
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(out.Bytes()), &envelope))
	assert.Equal(t, "sync.migration", envelope.Kind)
	assert.NotNil(t, got.migration)
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

	downloaded := map[string]int{}
	downloadCalls := 0
	archives := map[string]*os.File{}
	model := SyncCmd{Path: path, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloaded[pkg.Driver.Path]++
			downloadCalls++
			archive, err := downloadTestPkg(pkg)
			archives[pkg.Driver.Path] = archive
			return archive, err
		},
	}).(syncModel)
	model.worker.hooks.duringPrepare = func(_ context.Context, index int, _ installItem) error {
		if index == 1 {
			return errors.New("injected second driver prepare failure")
		}
		return nil
	}
	output := suite.runCmdErr(model)
	suite.Contains(output, "injected second driver prepare failure")

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
	for _, archive := range archives {
		assertFileClosed(suite.T(), archive)
	}
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
	assertFileClosed(suite.T(), downloadedArchive)
	assertNoPreparedWorkspaces(suite.T(), suite.Dir())
}

func (suite *SubcommandTestSuite) TestSyncLockMigrationNotificationFollowsCandidatePersist() {
	path := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
	legacyLock := "version = 1\n\n[[drivers]]\nname = \"test-driver-1\"\nversion = \"1.0.0\"\n"
	suite.Require().NoError(os.WriteFile(lockPath, []byte(legacyLock), 0o644))
	runPlain := func(model tea.Model) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var output bytes.Buffer
		program := tea.NewProgram(model, tea.WithInput(nil), tea.WithOutput(&output), tea.WithContext(ctx), tea.WithFilter(filterProgramMessage))
		prog = program
		defer func() { prog = nil }()
		programModel := tea.Model(model)
		finalModel, runErr := program.Run()
		notifyProgramExited(programModel)
		program.Wait()
		if runErr != nil {
			return output.String(), runErr
		}
		status := finalModel.(HasStatus)
		if err := status.Err(); err != nil {
			return output.String() + "\n" + formatErr(err), err
		}
		return output.String(), nil
	}

	model := SyncCmd{Path: path, NoVerify: true}.GetModelCustom(testBaseModel())
	first, err := runPlain(model)
	suite.Require().NoError(err)
	suite.Equal(1, strings.Count(first, "Migrated dbc.lock from v1 to v2."))

	// The successful migration leaves a v2 lock behind. Replaying it must not
	// produce another migration notification for the same lockfile.
	model = SyncCmd{Path: path, NoVerify: true}.GetModelCustom(testBaseModel())
	second, err := runPlain(model)
	suite.Require().NoError(err)
	suite.NotContains(second, "Migrated dbc.lock from v1 to v2.")

	// A failed candidate persist must not claim that migration happened, even
	// though the input lock was v1.
	suite.Require().NoError(os.WriteFile(lockPath, []byte(legacyLock), 0o644))
	failureModel := SyncCmd{Path: path, NoVerify: true}.GetModelCustom(testBaseModel()).(syncModel)
	failureModel.writeCandidateLock = func(string, LockFile) error { return errors.New("injected candidate save failure") }
	failure := suite.runCmdErr(failureModel)
	suite.NotContains(failure, "Migrated dbc.lock from v1 to v2.")
}

func (suite *SubcommandTestSuite) TestSyncLockMigrationNotificationSurvivesInstallFailure() {
	path := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
	suite.Require().NoError(os.WriteFile(lockPath, []byte("version = 1\n\n[[drivers]]\nname = \"test-driver-1\"\nversion = \"1.0.0\"\n"), 0o644))

	model := SyncCmd{Path: path, NoVerify: true}.GetModelCustom(testBaseModel()).(syncModel)
	model.worker.hooks.ensurePackage = func(context.Context, config.Config, string, config.ExpectedPackageMetadata, config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		return config.EnsurePackageResult{}, errors.New("injected install failure")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var output bytes.Buffer
	program := tea.NewProgram(model, tea.WithInput(nil), tea.WithOutput(&output), tea.WithContext(ctx), tea.WithFilter(filterProgramMessage))
	prog = program
	defer func() { prog = nil }()
	programModel := tea.Model(model)
	finalModel, runErr := program.Run()
	notifyProgramExited(programModel)
	program.Wait()
	suite.Require().NoError(runErr)
	suite.Equal(1, finalModel.(HasStatus).Status())
	out := output.String()
	suite.Equal(1, strings.Count(out, "Migrated dbc.lock from v1 to v2."))
	lock, err := loadLockFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(lockFileVersion, lock.Version, "candidate lock was persisted before install failure")
}

func (suite *SubcommandTestSuite) TestSyncInstallFailureKeepsCompleteCandidateLock() {
	path := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n[drivers.test-driver-no-sig]\n"), 0644))
	var downloadedDrivers []string
	var downloadedArchives []*os.File
	model := SyncCmd{Path: path, Level: suite.configLevel, NoVerify: true}.GetModelCustom(baseModel{
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
			downloadedDrivers = append(downloadedDrivers, pkg.Driver.Path)
			archive, err := os.Open(copyPath)
			if err == nil {
				downloadedArchives = append(downloadedArchives, archive)
			}
			return archive, err
		},
	}).(syncModel)
	ensureCalls := 0
	model.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, runtimeID string, expected config.ExpectedPackageMetadata, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		ensureCalls++
		if ensureCalls == 2 {
			return config.EnsurePackageResult{}, errors.New("injected package ensure failure")
		}
		return config.EnsurePackage(ctx, cfg, runtimeID, expected, callbacks)
	}
	suite.runCmdErr(model)

	lock, err := loadLockFile(lockPath)
	suite.Require().NoError(err)
	suite.Len(lock.lockinfo, 2)
	installedCount := 0
	for _, name := range []string{"test-driver-1", "test-driver-no-sig"} {
		if _, err := config.GetDriver(config.Get()[suite.configLevel], name); err == nil {
			installedCount++
		}
	}
	suite.Equal(1, installedCount)
	suite.Require().Len(downloadedDrivers, 2)
	suite.Equal(2, ensureCalls)
	suite.Require().Len(downloadedArchives, 2)
	for _, archive := range downloadedArchives {
		assertFileClosed(suite.T(), archive)
	}
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
	convergenceModel := SyncCmd{Path: path, Level: suite.configLevel, NoVerify: true}.GetModelCustom(baseModel{
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
		_, err := config.GetDriver(config.Get()[suite.configLevel], name)
		suite.NoError(err)
	}
	convergedLock, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(lockedCandidate, convergedLock)
}
