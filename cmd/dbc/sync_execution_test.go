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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	validation, err := config.PreparePackage(config.Config{Level: suite.configLevel, Location: suite.Dir()}, "test-driver-1", archive, expected, config.InstallOptions{})
	suite.Require().NoError(err)
	suite.Require().NotNil(validation.Prepared)
	suite.NoError(validation.Prepared.Close())
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

func TestPackageExecutorKeepsSourceArtifactMetadataInLock(t *testing.T) {
	for _, test := range []struct {
		name            string
		sourceType      string
		sourceReference string
		packageVersion  int
	}{
		{name: "registry metadata without package version", sourceType: "registry", sourceReference: "https://registry.example.test", packageVersion: 0},
		{name: "Packslip unspecified format", sourceType: "packslip", sourceReference: "github.com/example/driver", packageVersion: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "driver.tar.gz")
			archive, archiveHash := makeSyncPackageV2Archive(t, archivePath, "test-driver-1", "1.2.3", config.PlatformTuple())
			size := int64(len(archive))
			target := testTarget(config.PlatformTuple())
			release := resolution.ResolvedRelease{
				DriverID: "test-driver-1", Version: "1.2.3",
				Source: resolution.SourceSpec{Type: test.sourceType, Reference: test.sourceReference},
				Artifacts: []resolution.Artifact{{
					Target: target, Format: "tar.gz", PackageVersion: test.packageVersion,
					Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://assets.example.test/driver.tar.gz"},
					Hash:     archiveHash, Size: &size,
				}},
			}
			before, err := lockInfoFromResolvedRelease(release.DriverID, release)
			require.NoError(t, err)
			item, err := newInstallItem(release, 0, config.PlatformTuple(), nil)
			require.NoError(t, err)
			executor := newPackageExecutor(config.Config{Level: config.ConfigEnv, Location: filepath.Join(t.TempDir(), "install")}, t.TempDir(), true,
				func(context.Context, dbc.PkgInfo) (io.ReadCloser, error) { return os.Open(archivePath) }, nil, nil, nil)
			executor.fetchPackslip = func(context.Context, *url.URL) (io.ReadCloser, error) { return os.Open(archivePath) }
			require.NoError(t, executor.prepareItem(context.Background(), &item))
			require.NotNil(t, item.Validation)
			require.NoError(t, item.Validation.Prepared.Close())
			after, err := lockEntryForItem(item)
			require.NoError(t, err)
			require.Len(t, before.Artifacts, 1)
			require.Len(t, after.Artifacts, 1)
			assert.Equal(t, before.Artifacts[0].Hash, after.Artifacts[0].Hash)
			assert.Equal(t, before.Artifacts[0].Size, after.Artifacts[0].Size)
			assert.Equal(t, before.Artifacts[0].PackageVersion, after.Artifacts[0].PackageVersion)
		})
	}
}

func (suite *SubcommandTestSuite) TestSyncLegacyProofDoesNotSkipDifferentCandidateLibrary() {
	root := suite.T().TempDir()
	suite.T().Setenv("ADBC_DRIVER_PATH", root)
	driverListPath := filepath.Join(root, "dbc.toml")
	lockPath := filepath.Join(root, "dbc.lock")
	suite.Require().NoError(os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))

	legacyDirectory := filepath.Join(root, fmt.Sprintf("test-driver-1_%s_v1.1.0", config.PlatformTuple()))
	suite.Require().NoError(os.Mkdir(legacyDirectory, 0o755))
	legacyLibrary := filepath.Join(legacyDirectory, "driver.so")
	suite.Require().NoError(os.WriteFile(legacyLibrary, []byte("old legacy library"), 0o644))
	oldLibraryHash, err := checksum(legacyLibrary)
	suite.Require().NoError(err)
	legacyInfo := config.DriverInfo{
		ID: "test-driver-1", Name: "Legacy Test Driver", Version: semver.MustParse("1.1.0"), Source: "dbc",
	}
	legacyInfo.Driver.Shared.Set(config.PlatformTuple(), legacyLibrary)
	suite.Require().NoError(config.CreateManifest(config.Config{Level: config.ConfigEnv, Location: root}, legacyInfo))
	legacyLock := fmt.Sprintf("version = 1\n\n[[drivers]]\nname = %q\nversion = %q\nplatform = %q\nchecksum = %q\n", "test-driver-1", "1.1.0", config.PlatformTuple(), oldLibraryHash)
	suite.Require().NoError(os.WriteFile(lockPath, []byte(legacyLock), 0o644))

	downloadCalls := 0
	model := SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloadCalls++
			return os.Open(filepath.Join("testdata", "test-driver-1.1.tar.gz"))
		},
	})
	suite.runCmd(model)
	suite.Equal(1, downloadCalls)

	installed, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: root}, "test-driver-1")
	suite.Require().NoError(err)
	newLibrary := installed.Driver.Shared.Get(config.PlatformTuple())
	suite.NotEqual(legacyLibrary, newLibrary)
	newLibraryHash, err := checksum(newLibrary)
	suite.Require().NoError(err)
	suite.NotEqual(oldLibraryHash, newLibraryHash)
	updated, err := loadLockFile(lockPath)
	suite.Require().NoError(err)
	suite.Nil(updated.lockinfo["test-driver-1"].Legacy, "a mismatched v1 proof must not survive replacement by a different candidate library")
}

func (suite *SubcommandTestSuite) TestSyncLegacyManifestOnlyProofCompatibility() {
	type fixture struct {
		root, listPath, lockPath, archivePath, proofHash string
		oldLock                                          []byte
	}
	setup := func(t *testing.T, currentPath, candidatePath string, proofData, candidateData []byte) fixture {
		t.Helper()
		root := t.TempDir()
		t.Setenv("ADBC_DRIVER_PATH", root)
		driverListPath := filepath.Join(root, "dbc.toml")
		lockPath := filepath.Join(root, "dbc.lock")
		if err := os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeExternal := func(path string, data []byte) {
			t.Helper()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		writeExternal(currentPath, proofData)
		if err := os.WriteFile(filepath.Join(filepath.Dir(currentPath), "LICENSE"), []byte("external sibling"), 0o644); err != nil {
			t.Fatal(err)
		}
		if candidatePath != currentPath {
			writeExternal(candidatePath, candidateData)
			if err := os.WriteFile(filepath.Join(filepath.Dir(candidatePath), "LICENSE"), []byte("candidate sibling"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		current := config.DriverInfo{
			ID: "test-driver-1", Name: "Legacy Shared Driver", Version: semver.MustParse("1.1.0"), Source: "dbc",
		}
		current.Driver.Entrypoint = "DriverInit"
		current.Driver.Shared.Set(config.PlatformTuple(), currentPath)
		if err := config.CreateManifest(config.Config{Level: config.ConfigEnv, Location: root}, current); err != nil {
			t.Fatal(err)
		}
		proofFile := filepath.Join(t.TempDir(), "proof-library")
		if err := os.WriteFile(proofFile, proofData, 0o600); err != nil {
			t.Fatal(err)
		}
		proofHash, err := checksum(proofFile)
		if err != nil {
			t.Fatal(err)
		}
		legacyLock := fmt.Sprintf("version = 1\n\n[[drivers]]\nname = %q\nversion = %q\nplatform = %q\nchecksum = %q\n", "test-driver-1", "1.1.0", config.PlatformTuple(), proofHash)
		if err := os.WriteFile(lockPath, []byte(legacyLock), 0o644); err != nil {
			t.Fatal(err)
		}
		archivePath := filepath.Join(t.TempDir(), "manifest-only.tar.gz")
		archiveBytes := makeSyncManifestOnlyArchive(t, "1.1.0", candidatePath, "DriverInit")
		if err := os.WriteFile(archivePath, archiveBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		return fixture{
			root: root, listPath: driverListPath, lockPath: lockPath,
			archivePath: archivePath, proofHash: proofHash, oldLock: []byte(legacyLock),
		}
	}

	run := func(t *testing.T, driverListPath, archivePath string, count *syncArchiveRunCounts, registry func() ([]dbc.Driver, error)) (string, error) {
		t.Helper()
		model := SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(baseModel{
			getDriverRegistry: func() ([]dbc.Driver, error) {
				count.registry++
				return registry()
			},
			downloadPkg: func(dbc.PkgInfo) (*os.File, error) {
				count.download++
				return os.Open(archivePath)
			},
		}).(syncModel)
		model.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
			result, err := config.EnsurePackage(ctx, cfg, driver, expected, callbacks)
			if result.Manifest != nil {
				count.install++
			}
			return result, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var output bytes.Buffer
		program := tea.NewProgram(model, tea.WithInput(nil), tea.WithOutput(&output),
			tea.WithContext(ctx), tea.WithFilter(filterProgramMessage))
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
		var final string
		if finalOutput, ok := finalModel.(HasFinalOutput); ok {
			final = finalOutput.FinalOutput()
		}
		if err := status.Err(); err != nil {
			jsonMode := false
			if mode, ok := finalModel.(interface{ IsJSONMode() bool }); ok {
				jsonMode = mode.IsJSONMode()
			}
			if !jsonMode {
				final += "\n" + formatErr(err)
			}
		}
		combined := output.String() + final
		if status.Status() != 0 {
			return combined, status.Err()
		}
		return combined, nil
	}

	suite.Run("matching migration and locked replay", func() {
		t := suite.T()
		path := filepath.Join(t.TempDir(), "external", "library.so")
		data := []byte("legacy external library")
		fixture := setup(t, path, path, data, data)
		counts := &syncArchiveRunCounts{}
		out, err := run(t, fixture.listPath, fixture.archivePath, counts, getTestDriverRegistry)
		suite.NoError(err)
		suite.Contains(out, "Migrated dbc.lock from v1 to v2. v2 records package archive hashes; verified v1 installed-library checksums are retained as legacy proofs where applicable.")
		suite.Equal(syncArchiveRunCounts{registry: 1, download: 1, install: 0}, *counts)
		updated, err := loadLockFile(fixture.lockPath)
		suite.Require().NoError(err)
		suite.Require().NotNil(updated.lockinfo["test-driver-1"].Legacy)
		suite.Equal(fixture.proofHash, updated.lockinfo["test-driver-1"].Legacy.LibraryHash)
		installed, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: fixture.root}, "test-driver-1")
		suite.Require().NoError(err)
		suite.Equal(path, installed.Driver.Shared.Get(config.PlatformTuple()))
		suite.Equal("DriverInit", installed.Driver.Entrypoint)
		installedHash, err := checksum(path)
		suite.Require().NoError(err)
		suite.Equal(fixture.proofHash, installedHash)
		lockAfterMigration, err := os.ReadFile(fixture.lockPath)
		suite.Require().NoError(err)
		suite.NotEqual(fixture.oldLock, lockAfterMigration)

		replayCounts := &syncArchiveRunCounts{}
		_, err = run(t, fixture.listPath, fixture.archivePath, replayCounts, func() ([]dbc.Driver, error) {
			return nil, errors.New("locked v2 replay must not discover registry")
		})
		suite.NoError(err)
		suite.Equal(syncArchiveRunCounts{download: 1}, *replayCounts, "manifest-only locked replay validates its exact archive without registry discovery")
		lockAfterReplay, err := os.ReadFile(fixture.lockPath)
		suite.Require().NoError(err)
		suite.Equal(lockAfterMigration, lockAfterReplay)
	})

	suite.Run("mismatched candidate proof fails before mutation", func() {
		t := suite.T()
		root := t.TempDir()
		currentPath := filepath.Join(root, "external-current", "library.so")
		candidatePath := filepath.Join(root, "external-candidate", "library.so")
		proofData := []byte("preserved legacy library")
		candidateData := []byte("not the legacy library")
		fixture := setup(t, currentPath, candidatePath, proofData, candidateData)
		beforeManifest, err := os.ReadFile(filepath.Join(fixture.root, "test-driver-1.toml"))
		suite.Require().NoError(err)
		counts := &syncArchiveRunCounts{}
		out, err := run(t, fixture.listPath, fixture.archivePath, counts, getTestDriverRegistry)
		suite.Error(err)
		suite.Contains(out, "candidate package external library does not match the legacy lock proof")
		suite.Equal(1, counts.registry)
		suite.Equal(1, counts.download)
		suite.Equal(0, counts.install)
		lockAfter, err := os.ReadFile(fixture.lockPath)
		suite.Require().NoError(err)
		suite.Equal(fixture.oldLock, lockAfter)
		manifestAfter, err := os.ReadFile(filepath.Join(fixture.root, "test-driver-1.toml"))
		suite.Require().NoError(err)
		suite.Equal(beforeManifest, manifestAfter)
		currentAfter, err := os.ReadFile(currentPath)
		suite.Require().NoError(err)
		suite.Equal(proofData, currentAfter)
		candidateAfter, err := os.ReadFile(candidatePath)
		suite.Require().NoError(err)
		suite.Equal(candidateData, candidateAfter)
	})

}

type syncArchiveRunCounts struct {
	registry int
	download int
	install  int
}

func makeSyncManifestOnlyArchive(t *testing.T, version, sharedPath, entrypoint string) []byte {
	t.Helper()
	manifest := fmt.Sprintf(`manifest_version = 1
name = "Legacy Shared Driver"
version = %q

[Driver]
entrypoint = %q
shared = %q
`, version, entrypoint, sharedPath)
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "MANIFEST", Mode: 0o644, Size: int64(len(manifest)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte(manifest)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func (suite *SubcommandTestSuite) TestSyncPartialRegistryDownloadsEachArchiveOnceAndRejectsV2WithoutMetadata() {
	path := filepath.Join(suite.tempdir, "dbc.toml")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n[drivers.test-driver-no-sig]\n"), 0644))
	downloaded := map[string]int{}
	model := SyncCmd{Path: path, Level: suite.configLevel, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloaded[pkg.Driver.Path]++
			return downloadTestPkg(pkg)
		},
	})
	suite.runCmd(model)
	suite.Equal(map[string]int{"test-driver-1": 1, "test-driver-no-sig": 1}, downloaded)

	locked, err := loadLockFile(strings.TrimSuffix(path, ".toml") + ".lock")
	suite.Require().NoError(err)
	lockedArtifact, err := selectLockedArtifact(locked.lockinfo["test-driver-1"], config.PlatformTuple(), false)
	suite.Require().NoError(err)
	suite.NotEmpty(lockedArtifact.Hash, "legacy registry downloads finalize the archive hash")
	suite.Nil(lockedArtifact.Size, "host-measured registry size must not be added to the lock")
	installed, err := config.GetDriver(config.Config{Level: suite.configLevel, Location: suite.Dir()}, "test-driver-1")
	suite.Require().NoError(err)
	receipt, managed, present, valid, err := config.InspectDriverInstallReceipt(config.Config{Level: suite.configLevel, Location: suite.Dir()}, installed)
	suite.Require().NoError(err)
	suite.True(managed && present && valid)
	suite.Positive(receipt.ArchiveSize, "the receipt still retains the measured archive size")

	// The registry fixture omits archive metadata. A v2 archive must therefore
	// be rejected after its one download rather than treating measured values as
	// source-provided metadata.
	v2Path := filepath.Join(suite.tempdir, "v2-without-registry-metadata.tar.gz")
	makeSyncPackageV2Archive(suite.T(), v2Path, "test-driver-1", "1.1.0", config.PlatformTuple())
	v2List := filepath.Join(suite.tempdir, "v2.toml")
	suite.Require().NoError(os.WriteFile(v2List, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0644))
	v2Downloads := 0
	v2Model := SyncCmd{Path: v2List, Level: suite.configLevel, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(dbc.PkgInfo) (*os.File, error) {
			v2Downloads++
			return os.Open(v2Path)
		},
	})
	suite.Contains(suite.runCmdErr(v2Model), "package v2 requires archive hash metadata")
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

func writeSyncReceipt(t *testing.T, path string, receipt config.InstallReceipt) {
	t.Helper()
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (suite *SubcommandTestSuite) TestSyncManifestOnlyExternalLibraryWithoutProofRepairsFromExactLock() {
	root := suite.T().TempDir()
	suite.T().Setenv("ADBC_DRIVER_PATH", root)
	driverListPath := filepath.Join(root, "dbc.toml")
	suite.Require().NoError(os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
	suite.runCmd(SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(testBaseModel()))

	externalDir := filepath.Join(root, "external")
	suite.Require().NoError(os.Mkdir(externalDir, 0o755))
	externalLibrary := filepath.Join(externalDir, "driver.so")
	suite.Require().NoError(os.WriteFile(externalLibrary, []byte("external library"), 0o644))
	externalInfo := config.DriverInfo{
		ID: "test-driver-1", Name: "External Test Driver", Version: semver.MustParse("1.1.0"), Source: "dbc",
	}
	externalInfo.Driver.Shared.Set(config.PlatformTuple(), externalLibrary)
	suite.Require().NoError(config.CreateManifest(config.Config{Level: config.ConfigEnv, Location: root}, externalInfo))

	registryCalls, downloadCalls := 0, 0
	model := SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			registryCalls++
			return nil, errors.New("locked replay must not discover registries")
		},
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloadCalls++
			return downloadTestPkg(pkg)
		},
	})
	suite.runCmd(model)
	suite.Equal(0, registryCalls)
	suite.Equal(1, downloadCalls)
	got, err := os.ReadFile(externalLibrary)
	suite.Require().NoError(err)
	suite.Equal("external library", string(got))
	installed, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: root}, "test-driver-1")
	suite.Require().NoError(err)
	managedLibrary := installed.Driver.Shared.Get(config.PlatformTuple())
	suite.NotEqual(externalLibrary, managedLibrary)
	receipt, managed, present, valid, err := config.InspectDriverInstallReceipt(config.Config{Level: config.ConfigEnv, Location: root}, installed)
	suite.Require().NoError(err)
	suite.True(managed)
	suite.True(present)
	suite.True(valid)
	suite.True(config.VerifyInstallReceiptLibraryIntegrity(managedLibrary, receipt))
}

func (suite *SubcommandTestSuite) TestSyncManifestOnlyInstallUsesPreparedSnapshotAfterArchiveMutation() {
	root := suite.T().TempDir()
	suite.T().Setenv("ADBC_DRIVER_PATH", root)
	driverListPath := filepath.Join(root, "dbc.toml")
	lockPath := filepath.Join(root, "dbc.lock")
	externalLibrary := filepath.Join(root, "external", "driver.so")
	suite.Require().NoError(os.MkdirAll(filepath.Dir(externalLibrary), 0o755))
	suite.Require().NoError(os.WriteFile(externalLibrary, []byte("external runtime library"), 0o644))
	suite.Require().NoError(os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
	archiveBytes := makeSyncManifestOnlyArchive(suite.T(), "1.1.0", externalLibrary, "DriverInit")
	makeArchive := func() (*os.File, error) {
		archive, err := os.CreateTemp(root, "manifest-only-*.tar.gz")
		if err != nil {
			return nil, err
		}
		_, err = archive.Write(archiveBytes)
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		_, err = archive.Seek(0, io.SeekStart)
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		return archive, nil
	}
	var preparedArchivePath string
	var preparedPackage dbc.PkgInfo
	first := SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: getTestDriverRegistry,
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			preparedPackage = pkg
			archive, err := makeArchive()
			if err != nil {
				return nil, err
			}
			preparedArchivePath = archive.Name()
			return archive, nil
		},
	}).(syncModel)
	first.worker.hooks.beforeCandidateSave = func(context.Context) error {
		return os.WriteFile(preparedArchivePath, []byte("corrupted after validation"), 0o600)
	}
	suite.runCmd(first)
	suite.Equal("test-driver-1", preparedPackage.Driver.Path)
	_, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	locked, err := loadLockFile(lockPath)
	suite.Require().NoError(err)
	artifact, err := selectLockedArtifact(locked.lockinfo["test-driver-1"], config.PlatformTuple(), false)
	suite.Require().NoError(err)
	suite.NotEmpty(artifact.Hash)
	suite.Nil(artifact.Size, "registry downloads do not add measured size to the lock")
	installed, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: root}, "test-driver-1")
	suite.Require().NoError(err)
	suite.Equal(externalLibrary, installed.Driver.Shared.Get(config.PlatformTuple()))
	suite.Equal("DriverInit", installed.Driver.Entrypoint)
}

func (suite *SubcommandTestSuite) TestSyncPathReplayUsesReceiptBeforeOpeningChangedArchive() {
	root := suite.T().TempDir()
	suite.T().Setenv("ADBC_DRIVER_PATH", root)
	projectPath := filepath.Join(root, "dbc.toml")
	lockPath := filepath.Join(root, "dbc.lock")
	archivePath := filepath.Join(root, "packages", "driver.tar.gz")
	declaredPath := "./packages/driver.tar.gz"
	suite.Require().NoError(os.MkdirAll(filepath.Dir(archivePath), 0o700))
	makeSyncPackageV2Archive(suite.T(), archivePath, "test-driver-1", "1.2.3", config.PlatformTuple())
	suite.Require().NoError(os.WriteFile(projectPath, []byte("[drivers.test-driver-1.source]\ntype = 'path'\npath = '"+declaredPath+"'\n"), 0o600))

	registryCalls := 0
	newModel := func() syncModel {
		return SyncCmd{Path: projectPath, NoVerify: true}.GetModelCustom(baseModel{
			getDriverRegistry: func() ([]dbc.Driver, error) {
				registryCalls++
				return nil, errors.New("path lock replay must not discover registries")
			},
		}).(syncModel)
	}
	suite.runCmd(newModel())
	suite.Zero(registryCalls)
	lockBeforeReplay, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	locked, err := loadLockFile(lockPath)
	suite.Require().NoError(err)
	entry := locked.lockinfo["test-driver-1"]
	suite.Equal("path", entry.Source.Type)
	suite.Equal(declaredPath, entry.Source.Path)
	suite.Equal("1.2.3", entry.Version.String())
	suite.Require().Len(entry.Artifacts, 1)
	suite.Equal(declaredPath, entry.Artifacts[0].Location.Value)

	cfg := config.Config{Level: config.ConfigEnv, Location: root}
	installed, err := config.GetDriver(cfg, "test-driver-1")
	suite.Require().NoError(err)
	libraryPath := installed.Driver.Shared.Get(config.PlatformTuple())
	registrationPath := filepath.Join(root, "test-driver-1.toml")
	receiptPath := filepath.Join(filepath.Dir(libraryPath), "dbc-install-receipt.json")
	registrationBefore, err := os.ReadFile(registrationPath)
	suite.Require().NoError(err)
	receiptBefore, err := os.ReadFile(receiptPath)
	suite.Require().NoError(err)
	libraryBefore, err := os.ReadFile(libraryPath)
	suite.Require().NoError(err)

	// A healthy receipt and matching runtime registration allow exact lock replay
	// to skip without opening the path artifact, even if it has since changed.
	suite.Require().NoError(os.WriteFile(archivePath, []byte("changed archive bytes"), 0o600))
	second := newModel()
	second.worker.hooks.duringPrepare = func(_ context.Context, _ int, item installItem) error {
		if item.AlreadyInstalled == nil {
			return errors.New("valid path receipt should mark the locked package as already installed")
		}
		if item.Archive != nil {
			return errors.New("path artifact was opened despite a valid locked receipt")
		}
		return nil
	}
	ensureCalls := 0
	second.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		ensureCalls++
		result, err := config.EnsurePackage(ctx, cfg, driver, expected, callbacks)
		if err == nil && !result.Skipped {
			return result, errors.New("healthy path receipt should skip package installation")
		}
		return result, err
	}
	suite.runCmd(second)
	suite.Equal(1, ensureCalls)
	suite.Zero(registryCalls)
	lockAfterReplay, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(lockBeforeReplay, lockAfterReplay)
	registrationAfter, err := os.ReadFile(registrationPath)
	suite.Require().NoError(err)
	receiptAfter, err := os.ReadFile(receiptPath)
	suite.Require().NoError(err)
	libraryAfter, err := os.ReadFile(libraryPath)
	suite.Require().NoError(err)
	suite.Equal(registrationBefore, registrationAfter)
	suite.Equal(receiptBefore, receiptAfter)
	suite.Equal(libraryBefore, libraryAfter)

	// If repair is needed, the changed path must be reopened and the locked hash
	// mismatch must fail before either runtime state or lock state is published.
	var receipt config.InstallReceipt
	suite.Require().NoError(json.Unmarshal(receiptAfter, &receipt))
	receipt.SourceIdentity = "./packages/changed-driver.tar.gz"
	writeSyncReceipt(suite.T(), receiptPath, receipt)
	registrationBefore, err = os.ReadFile(registrationPath)
	suite.Require().NoError(err)
	receiptBefore, err = os.ReadFile(receiptPath)
	suite.Require().NoError(err)
	libraryBefore, err = os.ReadFile(libraryPath)
	suite.Require().NoError(err)

	third := newModel()
	ensureCalls = 0
	third.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		ensureCalls++
		return config.EnsurePackage(ctx, cfg, driver, expected, callbacks)
	}
	suite.Contains(suite.runCmdErr(third), "package archive hash mismatch")
	suite.Equal(0, ensureCalls, "archive validation must fail before installation")
	suite.Zero(registryCalls)
	lockAfterFailure, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(lockBeforeReplay, lockAfterFailure)
	registrationAfter, err = os.ReadFile(registrationPath)
	suite.Require().NoError(err)
	receiptAfterFailure, err := os.ReadFile(receiptPath)
	suite.Require().NoError(err)
	libraryAfter, err = os.ReadFile(libraryPath)
	suite.Require().NoError(err)
	suite.Equal(registrationBefore, registrationAfter)
	suite.Equal(receiptBefore, receiptAfterFailure)
	suite.Equal(libraryBefore, libraryAfter)
}

func (suite *SubcommandTestSuite) TestSyncPostDownloadReceiptMatchControlsSkip() {
	for _, test := range []struct {
		name          string
		mutateReceipt bool
		wantInstall   int
	}{
		{name: "matching receipt skips after validation", wantInstall: 0},
		{name: "mismatching receipt installs repair", mutateReceipt: true, wantInstall: 1},
	} {
		suite.Run(test.name, func() {
			t := suite.T()
			root := t.TempDir()
			t.Setenv("ADBC_DRIVER_PATH", root)
			driverListPath := filepath.Join(root, "dbc.toml")
			lockPath := filepath.Join(root, "dbc.lock")
			suite.Require().NoError(os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
			suite.runCmd(SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(testBaseModel()))

			cfg := config.Config{Level: config.ConfigEnv, Location: root}
			before, err := config.GetDriver(cfg, "test-driver-1")
			suite.Require().NoError(err)
			beforeLibrary := before.Driver.Shared.Get(config.PlatformTuple())
			if test.mutateReceipt {
				receiptPath := filepath.Join(filepath.Dir(beforeLibrary), "dbc-install-receipt.json")
				data, err := os.ReadFile(receiptPath)
				suite.Require().NoError(err)
				var receipt config.InstallReceipt
				suite.Require().NoError(json.Unmarshal(data, &receipt))
				receipt.SourceIdentity = "https://different.example"
				writeSyncReceipt(t, receiptPath, receipt)
			}
			suite.Require().NoError(os.Remove(lockPath), "force fresh registry resolution and candidate validation")

			registryCalls, downloadCalls, installCalls := 0, 0, 0
			model := SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(baseModel{
				getDriverRegistry: func() ([]dbc.Driver, error) {
					registryCalls++
					return getTestDriverRegistry()
				},
				downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
					downloadCalls++
					return downloadTestPkg(pkg)
				},
			}).(syncModel)
			model.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
				result, err := config.EnsurePackage(ctx, cfg, driver, expected, callbacks)
				if result.Manifest != nil {
					installCalls++
				}
				return result, err
			}
			suite.runCmd(model)
			suite.Equal(1, registryCalls)
			suite.Equal(1, downloadCalls)
			suite.Equal(test.wantInstall, installCalls)

			after, err := config.GetDriver(cfg, "test-driver-1")
			suite.Require().NoError(err)
			afterLibrary := after.Driver.Shared.Get(config.PlatformTuple())
			if test.wantInstall == 0 {
				suite.Equal(before.FilePath, after.FilePath)
				suite.Equal(beforeLibrary, afterLibrary, "a matching receipt must skip without replacing the registered generation")
			} else {
				suite.NotEqual(beforeLibrary, afterLibrary, "a mismatching receipt must trigger package replacement")
			}
			receipt, managed, present, valid, err := config.InspectDriverInstallReceipt(cfg, after)
			suite.Require().NoError(err)
			suite.True(managed)
			suite.True(present)
			suite.True(valid)
			suite.True(config.VerifyInstallReceiptLibraryIntegrity(afterLibrary, receipt))
			suite.Equal(testRegistry.BaseURL.String(), receipt.SourceIdentity)
		})
	}
}
