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
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/packslip"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/columnar-tech/dbc/internal/sourceresolution"
	"github.com/go-faster/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncProgressPercentTracksCompletedItems(t *testing.T) {
	if got := syncProgressPercent(0, 2); got != 0.5 {
		t.Fatalf("first completed item progress = %v, want 0.5", got)
	}
	if got := syncProgressPercent(1, 2); got != 1 {
		t.Fatalf("second completed item progress = %v, want 1", got)
	}
}

func TestFreshRegistryInstallItemDefaultsToTarGZWithoutHostRequirements(t *testing.T) {
	drivers, err := getTestDriverRegistry()
	require.NoError(t, err)
	model := syncModel{
		LockFilePath: filepath.Join(t.TempDir(), "dbc.lock"),
		driverIndex:  drivers,
	}
	planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{"test-driver-1": {}}})
	require.NoError(t, err)
	assert.True(t, needsRegistry)
	items, err := model.createInstallList(planned)
	require.NoError(t, err)
	require.Len(t, items, 1)
	selected, err := items[0].selectedArtifact()
	require.NoError(t, err)
	assert.Equal(t, "tar.gz", selected.Format)
	assert.Empty(t, selected.HostRequirements)
}

func TestNonRegistryRequirementDoesNotRequestRegistryDiscovery(t *testing.T) {
	for _, source := range []dbc.DriverSource{
		{Type: dbc.DriverSourcePackslip, Project: "github.com/example/driver"},
		{Type: dbc.DriverSourcePath, Path: "./driver.tar.gz"},
	} {
		t.Run(string(source.Type), func(t *testing.T) {
			var version *semver.Constraints
			if source.Type == dbc.DriverSourcePackslip {
				version, _ = semver.NewConstraint("1.2.3")
			}
			model := syncModel{LockFilePath: filepath.Join(t.TempDir(), "dbc.lock")}
			planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
				"test-driver-1": {Version: version, Source: &source},
			}})
			require.NoError(t, err)
			assert.False(t, needsRegistry)
			require.Len(t, planned, 1)
			assert.Equal(t, sourceresolution.PlanResolve, planned[0].Plan.Outcome())
		})
	}
}

func TestFreshPackslipResolutionSnapshotsAndReplaysWithoutDiscovery(t *testing.T) {
	const driverID = "test-driver-1"
	const version = "1.2.3+build.5"
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "driver.tgz")
	archiveBytes, archiveHash := makeSyncPackageV2Archive(t, archivePath, driverID, version, config.PlatformTuple())
	archiveSize := int64(len(archiveBytes))
	location := "https://assets.example.test/driver.tgz"
	release := makeSyncPackslipRelease(driverID, version, location, archiveHash, archiveSize)
	resolver := &syncPackslipResolverStub{release: release}
	constraint, err := semver.NewConstraint(version)
	require.NoError(t, err)
	source := dbc.DriverSource{Type: dbc.DriverSourcePackslip, Project: "github.com/example/driver"}
	list := DriversList{Drivers: map[string]driverSpec{driverID: {Version: constraint, Source: &source}}}
	projectPath := filepath.Join(dir, "dbc.toml")
	model := syncModel{
		baseModel: baseModel{
			newPackslipResolver: func() (packslip.Resolver, error) { return resolver, nil },
			fetchPackslipArtifact: func(_ context.Context, artifactURL *url.URL) (io.ReadCloser, error) {
				require.Equal(t, location, artifactURL.String())
				return os.Open(archivePath)
			},
		},
		Path: projectPath, LockFilePath: filepath.Join(dir, "dbc.lock"), NoVerify: true,
		cfg: config.Config{Level: config.ConfigEnv, Location: filepath.Join(dir, "install")},
	}

	planned, needsRegistry, err := model.planSyncItems(list)
	require.NoError(t, err)
	assert.False(t, needsRegistry)
	items, err := model.createInstallListContext(context.Background(), planned)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, 1, resolver.calls)
	assert.Equal(t, packslip.Request{DriverID: driverID, Version: version}, resolver.request)
	assert.Equal(t, release, items[0].Release, "verified source evidence and every signed artifact remain the execution input")

	prepared, err := model.prepareInstallItems(context.Background(), items)
	require.NoError(t, err)
	defer closePreparedItems(prepared.items)
	require.Len(t, prepared.lock.Drivers, 1)
	locked := prepared.lock.Drivers[0]
	assert.Equal(t, "packslip", locked.Source.Type)
	assert.Equal(t, source.Project, locked.Source.Project)
	assert.Equal(t, version, locked.Version.String())
	assert.Equal(t, release.Evidence[0].Hash, locked.Evidence[0].Hash)
	require.Len(t, locked.Artifacts, 2)
	lockedByLocation := make(map[string]lockArtifact, len(locked.Artifacts))
	for _, artifact := range locked.Artifacts {
		lockedByLocation[artifact.Location.Value] = artifact
	}
	primaryArtifact, ok := lockedByLocation[release.Artifacts[0].Location.Value]
	require.True(t, ok, "the lock retains the selected host artifact regardless of canonical artifact order")
	assert.Equal(t, release.Artifacts[0].Target, primaryArtifact.Target)
	assert.Zero(t, primaryArtifact.PackageVersion)
	assert.Equal(t, "tgz", primaryArtifact.Format)
	secondaryArtifact, ok := lockedByLocation[release.Artifacts[1].Location.Value]
	require.True(t, ok, "the lock retains the secondary artifact regardless of canonical artifact order")
	assert.Equal(t, release.Artifacts[1].Target, secondaryArtifact.Target)
	assert.Equal(t, "tar.gz", secondaryArtifact.Format)
	require.NoError(t, writeLockFileAtomic(model.LockFilePath, prepared.lock))

	var resolverConstructions, registryCalls, downloadCalls int
	replay := syncModel{
		baseModel: baseModel{
			getDriverRegistry: func() ([]dbc.Driver, error) {
				registryCalls++
				return nil, errors.New("replay must not discover registries")
			},
			newPackslipResolver: func() (packslip.Resolver, error) {
				resolverConstructions++
				return nil, errors.New("replay must not construct a resolver")
			},
			fetchPackslipArtifact: func(_ context.Context, artifactURL *url.URL) (io.ReadCloser, error) {
				downloadCalls++
				assert.Equal(t, location, artifactURL.String())
				return os.Open(archivePath)
			},
		},
		Path: projectPath, LockFilePath: model.LockFilePath, NoVerify: true,
		cfg: config.Config{Level: config.ConfigEnv, Location: filepath.Join(dir, "replay-install")},
	}
	replayPlan, replayNeedsRegistry, err := replay.planSyncItems(list)
	require.NoError(t, err)
	assert.False(t, replayNeedsRegistry)
	replayItems, err := replay.createInstallListContext(context.Background(), replayPlan)
	require.NoError(t, err)
	require.Len(t, replayItems, 1)
	assert.Zero(t, resolverConstructions)
	assert.Zero(t, registryCalls)
	replayedArtifact, err := replayItems[0].selectedArtifact()
	require.NoError(t, err)
	assert.Equal(t, archiveHash, replayedArtifact.Hash)
	assert.Equal(t, archiveSize, *replayedArtifact.Size)
	replayed, err := replay.prepareInstallItems(context.Background(), replayItems)
	require.NoError(t, err)
	defer closePreparedItems(replayed.items)
	assert.Equal(t, 1, downloadCalls)
	assert.ElementsMatch(t, prepared.lock.Drivers[0].Artifacts, replayed.lock.Drivers[0].Artifacts)
}

func assertFileClosed(t *testing.T, file *os.File) {
	t.Helper()
	_, err := file.Stat()
	assert.Error(t, err, "stat on a closed file must fail")
}

func assertNoPreparedWorkspaces(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	require.NoError(t, err)
	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), ".dbc-install-"), "unexpected prepared workspace %q", entry.Name())
	}
}

func TestPathResolutionDerivesVersionAndUsesProjectRelativeArchive(t *testing.T) {
	dir := t.TempDir()
	packagesDir := filepath.Join(dir, "packages")
	require.NoError(t, os.MkdirAll(packagesDir, 0o700))
	declaredPath := "./packages/test-driver-1.tar.gz"
	archiveBytes, err := os.ReadFile(filepath.Join("testdata", "test-driver-1.tar.gz"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(declaredPath, "./"))), archiveBytes, 0o600))
	cwd := t.TempDir()
	t.Chdir(cwd)
	source := dbc.DriverSource{Type: dbc.DriverSourcePath, Path: declaredPath}
	list := DriversList{Drivers: map[string]driverSpec{"test-driver-1": {Source: &source}}}
	model := syncModel{
		Path: filepath.Join(dir, "dbc.toml"), LockFilePath: filepath.Join(dir, "dbc.lock"), NoVerify: true,
		cfg: config.Config{Level: config.ConfigEnv, Location: filepath.Join(dir, "install")},
	}
	planned, needsRegistry, err := model.planSyncItems(list)
	require.NoError(t, err)
	assert.False(t, needsRegistry)
	items, err := model.createInstallListContext(context.Background(), planned)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "1.0.0", items[0].Release.Version)
	assert.Equal(t, declaredPath, items[0].Release.Source.Reference)
	require.Equal(t, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: declaredPath}, items[0].Release.Artifacts[0].Location)

	prepared, err := model.prepareInstallItems(context.Background(), items)
	require.NoError(t, err)
	defer closePreparedItems(prepared.items)
	locked := prepared.lock.Drivers[0]
	assert.Equal(t, "1.0.0", locked.Version.String())
	assert.Equal(t, declaredPath, locked.Source.Path)
	require.Len(t, locked.Artifacts, 1)
	assert.Equal(t, declaredPath, locked.Artifacts[0].Location.Value)
	assert.Equal(t, 0, locked.Artifacts[0].PackageVersion, "legacy MANIFEST path packages remain supported")
	assert.NotEmpty(t, locked.Artifacts[0].Hash)
}

func TestPathPackageV2MarkerSurvivesResolutionAndLockSnapshot(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "driver.tar.gz")
	makeSyncPackageV2Archive(t, archivePath, "test-driver-1", "1.2.3", config.PlatformTuple())
	source := dbc.DriverSource{Type: dbc.DriverSourcePath, Path: "driver.tar.gz"}
	model := syncModel{Path: filepath.Join(dir, "dbc.toml"), LockFilePath: filepath.Join(dir, "dbc.lock"), NoVerify: true,
		cfg: config.Config{Level: config.ConfigEnv, Location: filepath.Join(dir, "install")}}
	planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {Source: &source},
	}})
	require.NoError(t, err)
	assert.False(t, needsRegistry)
	items, err := model.createInstallListContext(context.Background(), planned)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, 2, items[0].Release.Artifacts[0].PackageVersion)
	assert.Equal(t, "1.2.3", items[0].Release.Version)
	prepared, err := model.prepareInstallItems(context.Background(), items)
	require.NoError(t, err)
	defer closePreparedItems(prepared.items)
	require.Len(t, prepared.lock.Drivers, 1)
	assert.Equal(t, 2, prepared.lock.Drivers[0].Artifacts[0].PackageVersion)
	expected, _, err := expectedSyncPackageMetadata(prepared.items[0])
	require.NoError(t, err)
	assert.Equal(t, 2, expected.PackageVersion)
}

func TestPathArchiveMutationAfterResolutionFailsBeforeCandidateLock(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "driver.tar.gz")
	archiveBytes, err := os.ReadFile(filepath.Join("testdata", "test-driver-1.tar.gz"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(archivePath, archiveBytes, 0o600))
	source := dbc.DriverSource{Type: dbc.DriverSourcePath, Path: "driver.tar.gz"}
	model := syncModel{Path: filepath.Join(dir, "dbc.toml"), LockFilePath: filepath.Join(dir, "dbc.lock"), NoVerify: true,
		cfg: config.Config{Level: config.ConfigEnv, Location: filepath.Join(dir, "install")}}
	planned, _, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{"test-driver-1": {Source: &source}}})
	require.NoError(t, err)
	items, err := model.createInstallListContext(context.Background(), planned)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(archivePath, append(archiveBytes, []byte("changed")...), 0o600))
	prepared, err := model.prepareInstallItems(context.Background(), items)
	defer closePreparedItems(prepared.items)
	require.ErrorContains(t, err, "package archive hash mismatch")
	assert.NoFileExists(t, model.LockFilePath)
}

func TestPathMetadataDerivedRefreshAdoptsArchiveVersionWithoutOldSnapshotProof(t *testing.T) {
	dir := t.TempDir()
	const declaredPath = "./packages/driver.tar.gz"
	archivePath := filepath.Join(dir, "packages", "driver.tar.gz")
	require.NoError(t, os.MkdirAll(filepath.Dir(archivePath), 0o700))
	_, newHash := makeSyncPackageV2Archive(t, archivePath, "test-driver-1", "1.2.3", config.PlatformTuple())
	stat, err := os.Stat(archivePath)
	require.NoError(t, err)
	oldEntry := partialPathLockForSync(t, declaredPath, "1.1.0", "sha256:"+strings.Repeat("b", 64), 17)
	lockPath := filepath.Join(dir, "dbc.lock")
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{oldEntry}}))
	oldLockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	source := dbc.DriverSource{Type: dbc.DriverSourcePath, Path: declaredPath}
	model := syncModel{Path: filepath.Join(dir, "dbc.toml"), LockFilePath: lockPath, NoVerify: true,
		cfg: config.Config{Level: config.ConfigEnv, Location: filepath.Join(dir, "install")}}
	planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {Source: &source},
	}})
	require.NoError(t, err)
	assert.False(t, needsRegistry)
	require.Len(t, planned, 1)
	assert.Equal(t, sourceresolution.PlanRefreshRequired, planned[0].Plan.Outcome())
	items, err := model.createInstallListContext(context.Background(), planned)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "1.2.3", items[0].Release.Version, "metadata-derived refresh follows the current archive")
	assert.Nil(t, items[0].LockEntry, "old-version snapshot evidence must not reach a different-version candidate")

	prepared, err := model.prepareInstallItems(context.Background(), items)
	require.NoError(t, err)
	defer closePreparedItems(prepared.items)
	require.Len(t, prepared.lock.Drivers, 1)
	candidate := prepared.lock.Drivers[0]
	assert.Equal(t, "1.2.3", candidate.Version.String())
	assert.Empty(t, candidate.Evidence, "old release evidence must not be merged")
	assert.Nil(t, candidate.Legacy, "old legacy proof must not be merged")
	require.Len(t, candidate.Artifacts, 1, "old target artifacts must not be carried into the changed release")
	assert.Equal(t, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: declaredPath}, candidate.Artifacts[0].Location)
	assert.Equal(t, newHash, candidate.Artifacts[0].Hash)
	assert.Equal(t, stat.Size(), *candidate.Artifacts[0].Size)
	assert.NotEqual(t, oldEntry.Artifacts[0].Hash, candidate.Artifacts[0].Hash)
	lockAfterPrepare, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assert.Equal(t, oldLockBytes, lockAfterPrepare, "prepare does not persist the candidate over the existing lock")
}

func TestPathExactRefreshStillRejectsChangedArchiveVersion(t *testing.T) {
	dir := t.TempDir()
	const declaredPath = "./packages/driver.tar.gz"
	archivePath := filepath.Join(dir, "packages", "driver.tar.gz")
	require.NoError(t, os.MkdirAll(filepath.Dir(archivePath), 0o700))
	makeSyncPackageV2Archive(t, archivePath, "test-driver-1", "1.2.3", config.PlatformTuple())
	oldEntry := partialPathLockForSync(t, declaredPath, "1.1.0", "sha256:"+strings.Repeat("b", 64), 17)
	lockPath := filepath.Join(dir, "dbc.lock")
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{oldEntry}}))
	oldLockBytes, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	version, err := semver.NewConstraint("1.1.0")
	require.NoError(t, err)
	source := dbc.DriverSource{Type: dbc.DriverSourcePath, Path: declaredPath}
	model := syncModel{Path: filepath.Join(dir, "dbc.toml"), LockFilePath: lockPath}
	planned, _, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {Version: version, Source: &source},
	}})
	require.NoError(t, err)
	require.Equal(t, sourceresolution.PlanRefreshRequired, planned[0].Plan.Outcome())
	_, err = model.createInstallListContext(context.Background(), planned)
	require.ErrorContains(t, err, "does not match requested version")
	afterFailure, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assert.Equal(t, oldLockBytes, afterFailure, "exact-version rejection leaves the existing lock untouched")
}

func partialPathLockForSync(t *testing.T, declaredPath, version, hash string, size int64) lockInfo {
	t.Helper()
	release := resolution.ResolvedRelease{
		DriverID: "test-driver-1",
		Version:  version,
		Source:   resolution.SourceSpec{Type: "path", Reference: declaredPath},
		Evidence: []resolution.Evidence{{
			Kind:     resolution.EvidenceKindReleaseMetadata,
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/old-release.json"},
			Hash:     "sha256:" + strings.Repeat("d", 64),
		}},
		Artifacts: []resolution.Artifact{{
			Target: resolution.Target{OS: "plan9", Arch: "amd64"}, Format: "tar.gz", PackageVersion: 2,
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: declaredPath},
			Hash:     hash, Size: &size,
		}},
	}
	entry, err := lockInfoFromResolvedRelease(release.DriverID, release)
	require.NoError(t, err)
	entry.Legacy = &legacyLibraryProof{Platform: config.PlatformTuple(), LibraryHash: strings.Repeat("c", 64)}
	return entry
}

func TestSourceVersionOrIdentityMismatchDiscardsOldLockProof(t *testing.T) {
	entry := testResolvedRelease()
	entry.DriverID = "test-driver-1"
	entry.Source = resolution.SourceSpec{Type: "packslip", Reference: "github.com/example/old"}
	entry.Version = "1.2.3+foo"
	entry.Artifacts[0].PackageVersion = 2
	oldLock, err := lockInfoFromResolvedRelease(entry.DriverID, entry)
	require.NoError(t, err)
	oldLock.Legacy = &legacyLibraryProof{Platform: config.PlatformTuple(), LibraryHash: strings.Repeat("c", 64)}
	lockPath := filepath.Join(t.TempDir(), "dbc.lock")
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{oldLock}}))

	constraint, err := semver.NewConstraint("1.2.3+bar")
	require.NoError(t, err)
	newSource := dbc.DriverSource{Type: dbc.DriverSourcePackslip, Project: "github.com/example/new"}
	model := syncModel{LockFilePath: lockPath}
	planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {Version: constraint, Source: &newSource},
	}})
	require.NoError(t, err)
	assert.False(t, needsRegistry)
	require.Len(t, planned, 1)
	assert.Equal(t, sourceresolution.PlanResolve, planned[0].Plan.Outcome())
	assert.Nil(t, planned[0].LockEntry, "stale source/version metadata must not enter the new candidate")
	assert.Nil(t, planned[0].LegacyLock)
}

func TestRegistrySourceChangeDiscardsOldLockEntryBeforeFallback(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "dbc.lock")
	entry := testRegistryLockEntryForPlatform(config.PlatformTuple())
	entry.Name = "test-driver-1"
	entry.Version = semver.MustParse("1.0.0")
	entry.Source.URL = "https://registry-a.example.test"
	entry.Evidence = []lockEvidence{{
		Kind:     resolution.EvidenceKindReleaseMetadata,
		Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://registry-a.example.test/release.json"},
		Hash:     "sha256:" + strings.Repeat("b", 64),
	}}
	entry.Legacy = &legacyLibraryProof{Platform: config.PlatformTuple(), LibraryHash: strings.Repeat("c", 64)}
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))

	registryBURL, err := url.Parse("https://registry-b.example.test")
	require.NoError(t, err)
	registryB := testRegistry
	registryB.BaseURL = registryBURL
	drivers, err := getTestDriverRegistry()
	require.NoError(t, err)
	for i := range drivers {
		if drivers[i].Path == "test-driver-1" {
			drivers[i].Registry = &registryB
		}
	}

	list := DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {
			Source: &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: registryBURL.String()},
		},
	}}
	model := syncModel{LockFilePath: lockPath, driverIndex: drivers}
	planned, needsRegistry, err := model.planSyncItems(list)
	require.NoError(t, err)
	assert.True(t, needsRegistry, "a lock for registry A cannot be reused for registry B")

	for _, plan := range planned {
		assert.Equal(t, sourceresolution.PlanResolve, plan.Plan.Outcome())
		assert.Nil(t, plan.LockEntry, "the old registry A entry must not influence registry B planning")
	}
	items, err := model.createInstallList(planned)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Nil(t, items[0].LockEntry, "the old registry A entry must not be merged into a registry B candidate")
	assert.Equal(t, registryBURL.String(), items[0].Release.Source.Reference)
}

func TestRegistryVersionMismatchDoesNotCarryOldReleaseEvidenceOrLegacyProof(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "dbc.lock")
	entry := testRegistryLockEntryForPlatform(config.PlatformTuple())
	entry.Name = "test-driver-1"
	entry.Version = semver.MustParse("1.0.0")
	entry.Source.URL = testRegistry.BaseURL.String()
	entry.Evidence = []lockEvidence{{
		Kind:     resolution.EvidenceKindReleaseMetadata,
		Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://registry.example.test/old-release.json"},
		Hash:     "sha256:" + strings.Repeat("b", 64),
	}}
	entry.Legacy = &legacyLibraryProof{Platform: config.PlatformTuple(), LibraryHash: strings.Repeat("c", 64)}
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))

	constraint, err := semver.NewConstraint("=1.1.0")
	require.NoError(t, err)
	model := syncModel{LockFilePath: lockPath, driverIndex: mustTestRegistryDrivers(t)}
	planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {Version: constraint},
	}})
	require.NoError(t, err)
	assert.True(t, needsRegistry)
	require.Len(t, planned, 1)
	assert.Equal(t, sourceresolution.PlanResolve, planned[0].Plan.Outcome())
	assert.Nil(t, planned[0].LockEntry, "a version-mismatched v2 snapshot is not refresh input")
	assert.Nil(t, planned[0].LegacyLock, "v2 proof cannot be reinterpreted as a v1 migration proof")

	items, err := model.createInstallList(planned)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Nil(t, items[0].LockEntry, "the mismatched release evidence must not reach the candidate")
	assert.Equal(t, "1.1.0", items[0].Release.Version)
}

func TestRegistryTargetRefreshRetainsMatchingReleaseSnapshot(t *testing.T) {
	otherPlatform := "linux_amd64"
	if config.PlatformTuple() == otherPlatform {
		otherPlatform = "macos_arm64"
	}
	entry := testRegistryLockEntryForPlatform(otherPlatform)
	entry.Name = "test-driver-1"
	entry.Source.URL = testRegistry.BaseURL.String()
	lockPath := filepath.Join(t.TempDir(), "dbc.lock")
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))
	model := syncModel{LockFilePath: lockPath, driverIndex: mustTestRegistryDrivers(t)}
	planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {},
	}})
	require.NoError(t, err)
	assert.True(t, needsRegistry)
	require.Len(t, planned, 1)
	assert.Equal(t, sourceresolution.PlanRefreshRequired, planned[0].Plan.Outcome())
	require.NotNil(t, planned[0].LockEntry, "a matching release snapshot may supply prior artifacts for target refresh")
}

func TestDefaultRegistryRefreshStaysPinnedToLockedRegistry(t *testing.T) {
	otherPlatform := "linux_amd64"
	if config.PlatformTuple() == otherPlatform {
		otherPlatform = "macos_arm64"
	}
	entry := testRegistryLockEntryForPlatform(otherPlatform)
	entry.Name = "test-driver-1"
	entry.Version = semver.MustParse("1.1.0")
	entry.Source.URL = "https://registry-b.example.test"
	lockPath := filepath.Join(t.TempDir(), "dbc.lock")
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))

	registryA := registryScopedTestDriver(t, "test-driver-1", "https://registry-a.example.test")
	registryB := registryScopedTestDriver(t, "test-driver-1", "https://registry-b.example.test")
	model := syncModel{LockFilePath: lockPath, driverIndex: []dbc.Driver{registryA, registryB}}
	planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {},
	}})
	require.NoError(t, err)
	assert.True(t, needsRegistry)
	require.Len(t, planned, 1)
	assert.Equal(t, sourceresolution.PlanRefreshRequired, planned[0].Plan.Outcome())

	items, err := model.createInstallList(planned)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "https://registry-b.example.test", items[0].Release.Source.Reference,
		"a refresh must resolve missing target metadata from the source already named by the lock")
	require.NotNil(t, items[0].LockEntry)
	assert.Equal(t, "https://registry-b.example.test", items[0].LockEntry.Source.URL)
	selected, err := items[0].selectedArtifact()
	require.NoError(t, err)
	assert.Contains(t, selected.Location.Value, "registry-b.example.test")
}

func TestDefaultRegistryRefreshDoesNotFallbackWhenLockedRegistryLacksDriver(t *testing.T) {
	otherPlatform := "linux_amd64"
	if config.PlatformTuple() == otherPlatform {
		otherPlatform = "macos_arm64"
	}
	entry := testRegistryLockEntryForPlatform(otherPlatform)
	entry.Name = "test-driver-1"
	entry.Version = semver.MustParse("1.1.0")
	entry.Source.URL = "https://registry-b.example.test"
	lockPath := filepath.Join(t.TempDir(), "dbc.lock")
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))
	before, err := os.ReadFile(lockPath)
	require.NoError(t, err)

	registryA := registryScopedTestDriver(t, "test-driver-1", "https://registry-a.example.test")
	registryBWithoutDriver := registryScopedTestDriver(t, "test-driver-2", "https://registry-b.example.test")
	model := syncModel{LockFilePath: lockPath, driverIndex: []dbc.Driver{registryA, registryBWithoutDriver}}
	planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {},
	}})
	require.NoError(t, err)
	assert.True(t, needsRegistry)
	require.Len(t, planned, 1)
	assert.Equal(t, sourceresolution.PlanRefreshRequired, planned[0].Plan.Outcome())

	items, err := model.createInstallList(planned)
	require.ErrorContains(t, err, "was not found in locked registry source")
	assert.Empty(t, items, "resolution must fail before the package enters preparation")
	after, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a failed pinned-source refresh cannot mutate the existing lock")
}

func TestV1ProofIsNotCarriedAcrossUndeclaredToExplicitRegistrySource(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "dbc.lock")
	proof := strings.Repeat("d", 64)
	body := fmt.Sprintf("version = 1\n\n[[drivers]]\nname = %q\nversion = %q\nplatform = %q\nchecksum = %q\n",
		"test-driver-1", "1.1.0", config.PlatformTuple(), proof)
	require.NoError(t, os.WriteFile(lockPath, []byte(body), 0o600))

	constraint, err := semver.NewConstraint("=1.1.0")
	require.NoError(t, err)
	list := DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {
			Version: constraint,
			Source:  &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: testRegistry.BaseURL.String()},
		},
	}}
	model := syncModel{LockFilePath: lockPath, driverIndex: mustTestRegistryDrivers(t)}
	planned, needsRegistry, err := model.planSyncItems(list)
	require.NoError(t, err)
	assert.True(t, needsRegistry)
	require.Len(t, planned, 1)
	assert.Nil(t, planned[0].LegacyLock, "v1 has no source key to prove it came from the newly explicit registry")

	items, err := model.createInstallList(planned)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "1.1.0", items[0].Release.Version)
	assert.Nil(t, items[0].LockEntry, "unattributed v1 library proof must not survive into the candidate")
}

func TestExplicitPrereleaseConstraintSyncsThroughReplayRefreshAndV1Migration(t *testing.T) {
	constraint, err := semver.NewConstraint("=2.1.0-beta.1")
	require.NoError(t, err)
	list := DriversList{Drivers: map[string]driverSpec{
		"test-driver-2": {Version: constraint},
	}}
	drivers := mustTestRegistryDrivers(t)

	t.Run("fresh resolve", func(t *testing.T) {
		model := syncModel{LockFilePath: filepath.Join(t.TempDir(), "dbc.lock"), driverIndex: drivers}
		planned, needsRegistry, err := model.planSyncItems(list)
		require.NoError(t, err)
		assert.True(t, needsRegistry)
		items, err := model.createInstallList(planned)
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, "2.1.0-beta.1", items[0].Release.Version)
	})

	t.Run("complete v2 replay", func(t *testing.T) {
		entry := testRegistryLockEntryForPlatform(config.PlatformTuple())
		entry.Name = "test-driver-2"
		entry.Version = semver.MustParse("2.1.0-beta.1")
		entry.Source.URL = testRegistry.BaseURL.String()
		lockPath := filepath.Join(t.TempDir(), "dbc.lock")
		require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))
		model := syncModel{LockFilePath: lockPath}
		planned, needsRegistry, err := model.planSyncItems(list)
		require.NoError(t, err)
		assert.False(t, needsRegistry)
		items, err := model.createInstallList(planned)
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, "2.1.0-beta.1", items[0].Release.Version)
	})

	t.Run("target refresh", func(t *testing.T) {
		otherPlatform := "linux_amd64"
		if config.PlatformTuple() == otherPlatform {
			otherPlatform = "macos_arm64"
		}
		entry := testRegistryLockEntryForPlatform(otherPlatform)
		entry.Name = "test-driver-2"
		entry.Version = semver.MustParse("2.1.0-beta.1")
		entry.Source.URL = testRegistry.BaseURL.String()
		lockPath := filepath.Join(t.TempDir(), "dbc.lock")
		require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))
		model := syncModel{LockFilePath: lockPath, driverIndex: drivers}
		planned, needsRegistry, err := model.planSyncItems(list)
		require.NoError(t, err)
		assert.True(t, needsRegistry)
		require.Len(t, planned, 1)
		assert.Equal(t, sourceresolution.PlanRefreshRequired, planned[0].Plan.Outcome())
		items, err := model.createInstallList(planned)
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, "2.1.0-beta.1", items[0].Release.Version)
		require.NotNil(t, items[0].LockEntry)
	})

	t.Run("v1 migration", func(t *testing.T) {
		lockPath := filepath.Join(t.TempDir(), "dbc.lock")
		proof := strings.Repeat("d", 64)
		body := fmt.Sprintf("version = 1\n\n[[drivers]]\nname = %q\nversion = %q\nplatform = %q\nchecksum = %q\n",
			"test-driver-2", "2.1.0-beta.1", config.PlatformTuple(), proof)
		require.NoError(t, os.WriteFile(lockPath, []byte(body), 0o600))
		model := syncModel{LockFilePath: lockPath, driverIndex: drivers}
		planned, needsRegistry, err := model.planSyncItems(list)
		require.NoError(t, err)
		assert.True(t, needsRegistry)
		items, err := model.createInstallList(planned)
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, "2.1.0-beta.1", items[0].Release.Version)
		require.NotNil(t, items[0].LockEntry)
		require.NotNil(t, items[0].LockEntry.Legacy)
		assert.Equal(t, proof, items[0].LockEntry.Legacy.LibraryHash)
	})
}

func TestRegistryReplayPlansDefaultAndExplicitSourceWithoutDiscovery(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "default"
		if explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			entry := testRegistryLockEntryForPlatform(config.PlatformTuple())
			entry.Name = "test-driver-1"
			entry.Source.URL = testRegistry.BaseURL.String()
			lockPath := filepath.Join(t.TempDir(), "dbc.lock")
			require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))
			spec := driverSpec{}
			if explicit {
				spec.Source = &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: testRegistry.BaseURL.String()}
			}
			model := syncModel{LockFilePath: lockPath}
			planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{"test-driver-1": spec}})
			require.NoError(t, err)
			assert.False(t, needsRegistry)
			items, err := model.createInstallList(planned)
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.NotNil(t, items[0].LockEntry)
			assert.Nil(t, model.driverIndex, "complete v2 replay must not require registry discovery")
		})
	}
}

func TestRegistryPackageAdapterRejectsDifferentSourceIdentity(t *testing.T) {
	driver := registryScopedTestDriver(t, "test-driver-1", "https://registry-a.example.test")
	other := registryScopedTestDriver(t, "test-driver-1", "https://registry-b.example.test")
	archiveURL, err := url.Parse("https://assets.example.test/archive.tar.gz")
	require.NoError(t, err)
	_, err = resolvedReleaseFromRegistryPackage(driver, dbc.PkgInfo{
		Driver:        other,
		Version:       semver.MustParse("1.1.0"),
		PlatformTuple: config.PlatformTuple(),
		Path:          archiveURL,
	})
	require.ErrorContains(t, err, "different source")
}

func TestRegistrySyncUsesOnlyExactReleaseMetadataForAllTargetLock(t *testing.T) {
	for _, completeMetadata := range []bool{true, false} {
		name := "complete metadata"
		if !completeMetadata {
			name = "missing non-host hash keeps partial lock"
		}
		t.Run(name, func(t *testing.T) {
			const driverID, version = "test-driver-1", "1.2.3"
			dir := t.TempDir()
			archivePath := filepath.Join(dir, "host.tar.gz")
			_, hostHash := makeSyncPackageV2Archive(t, archivePath, driverID, version, config.PlatformTuple())
			platforms := []string{"windows_amd64", "linux_amd64", "darwin_arm64"}
			var otherPlatform, missingHashPlatform string
			for _, platform := range platforms {
				if platform == config.PlatformTuple() {
					continue
				}
				if otherPlatform == "" {
					otherPlatform = platform
				} else {
					missingHashPlatform = platform
					break
				}
			}
			require.NotEmpty(t, otherPlatform)
			require.NotEmpty(t, missingHashPlatform)
			missingHashLine := "            hash: sha256:" + strings.Repeat("c", 64) + "\n"
			if !completeMetadata {
				missingHashLine = ""
			}
			index := fmt.Sprintf(`drivers:
  - name: Test Driver
    description: synthetic multi-target registry fixture
    license: MIT
    path: %s
    pkginfo:
      - version: %s
        packages:
          - platform: %s
            url: host.tar.gz
            hash: %s
            future_metadata: ignored
          - platform: %s
            url: other.tar.gz
            hash: sha256:%s
          - platform: %s
            url: missing-hash.tar.gz

%s`, driverID, version, config.PlatformTuple(), hostHash,
				otherPlatform, strings.Repeat("b", 64), missingHashPlatform, missingHashLine)
			var fixture struct {
				Drivers []dbc.Driver `yaml:"drivers"`
			}
			require.NoError(t, yaml.NewDecoder(strings.NewReader(index)).Decode(&fixture))
			require.Len(t, fixture.Drivers, 1)
			registryURL, err := url.Parse("https://registry.example.test")
			require.NoError(t, err)
			fixture.Drivers[0].Registry = &dbc.Registry{BaseURL: registryURL}

			constraint, err := semver.NewConstraint(version)
			require.NoError(t, err)
			source := dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: registryURL.String()}
			var downloaded []string
			model := syncModel{
				baseModel: baseModel{
					downloadArtifact: func(_ context.Context, pkg dbc.PkgInfo) (io.ReadCloser, error) {
						downloaded = append(downloaded, pkg.PlatformTuple)
						return os.Open(archivePath)
					},
				},
				Path: filepath.Join(dir, "dbc.toml"), LockFilePath: filepath.Join(dir, "dbc.lock"),
				NoVerify: true, driverIndex: fixture.Drivers,
				cfg: config.Config{Level: config.ConfigEnv, Location: filepath.Join(dir, "install")},
			}
			planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
				driverID: {Version: constraint, Source: &source},
			}})
			require.NoError(t, err)
			assert.True(t, needsRegistry, "the registry source requires registry resolution")
			items, err := model.createInstallList(planned)
			require.NoError(t, err)
			require.Len(t, items, 1)
			assert.Empty(t, downloaded, "planning must use index metadata without fetching archives")
			if completeMetadata {
				require.Len(t, items[0].Release.Artifacts, 3)
				var nonHost *resolution.Artifact
				for i := range items[0].Release.Artifacts {
					if items[0].Release.Artifacts[i].Target != items[0].Release.Artifacts[items[0].ArtifactIndex].Target {
						nonHost = &items[0].Release.Artifacts[i]
					}
				}
				require.NotNil(t, nonHost)
				assert.Equal(t, "sha256:"+strings.Repeat("b", 64), nonHost.Hash)
				assert.Nil(t, nonHost.Size, "hash-only registry metadata must not invent a size")
			} else {
				require.Len(t, items[0].Release.Artifacts, 2)
			}

			prepared, err := model.prepareInstallItems(context.Background(), items)
			require.NoError(t, err)
			defer closePreparedItems(prepared.items)
			assert.Equal(t, []string{config.PlatformTuple()}, downloaded, "only the current host artifact may be fetched")
			wantArtifacts := 2 // Host plus the hash-bearing non-host target.
			if completeMetadata {
				wantArtifacts = 3
			}
			require.Len(t, prepared.lock.Drivers, 1)
			require.Len(t, prepared.lock.Drivers[0].Artifacts, wantArtifacts)
			if completeMetadata {
				var nonHost *lockArtifact
				for i := range prepared.lock.Drivers[0].Artifacts {
					if prepared.lock.Drivers[0].Artifacts[i].Target != items[0].Release.Artifacts[items[0].ArtifactIndex].Target {
						nonHost = &prepared.lock.Drivers[0].Artifacts[i]
					}
				}
				require.NotNil(t, nonHost)
				assert.Contains(t, []string{
					"sha256:" + strings.Repeat("b", 64),
					"sha256:" + strings.Repeat("c", 64),
				}, nonHost.Hash)
				assert.Nil(t, nonHost.Size)
			}
		})
	}
}

func mustTestRegistryDrivers(t *testing.T) []dbc.Driver {
	t.Helper()
	drivers, err := getTestDriverRegistry()
	require.NoError(t, err)
	return drivers
}

func registryScopedTestDriver(t *testing.T, driverID, registryURL string) dbc.Driver {
	t.Helper()
	drivers, err := getTestDriverRegistry()
	require.NoError(t, err)
	baseURL, err := url.Parse(registryURL)
	require.NoError(t, err)
	for _, driver := range drivers {
		if driver.Path != driverID {
			continue
		}
		registry := *driver.Registry
		registry.BaseURL = baseURL
		driver.Registry = &registry
		return driver
	}
	t.Fatalf("test registry has no driver %q", driverID)
	return dbc.Driver{}
}

func TestCreateInstallListSelectsDriverFromDeclaredRegistry(t *testing.T) {
	registryA := registryScopedTestDriver(t, "test-driver-1", "https://registry-a.example.test")
	registryB := registryScopedTestDriver(t, "test-driver-1", "https://registry-b.example.test")
	model := syncModel{
		LockFilePath: filepath.Join(t.TempDir(), "dbc.lock"),
		driverIndex:  []dbc.Driver{registryA, registryB},
	}
	declaredSource := &dbc.DriverSource{
		Type: dbc.DriverSourceRegistry,
		URL:  "HTTPS://REGISTRY-B.EXAMPLE.TEST/",
	}
	planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {Source: declaredSource},
	}})
	require.NoError(t, err)
	assert.True(t, needsRegistry)
	items, err := model.createInstallList(planned)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "https://registry-b.example.test", items[0].Release.Source.Reference)
	expected, _, err := expectedSyncPackageMetadata(items[0])
	require.NoError(t, err)
	assert.Equal(t, "https://registry-b.example.test", expected.SourceIdentity)
}

func TestCreateInstallListDoesNotFallbackOutsideDeclaredRegistry(t *testing.T) {
	registryA := registryScopedTestDriver(t, "test-driver-1", "https://registry-a.example.test")
	registryBWithoutDriver := registryScopedTestDriver(t, "test-driver-2", "https://registry-b.example.test")
	tests := []struct {
		name string
		url  string
	}{
		{name: "driver missing in declared registry", url: "https://registry-b.example.test"},
		{name: "path mismatch", url: "https://registry-b.example.test/tenant"},
		{name: "query mismatch", url: "https://registry-b.example.test?tenant=b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := syncModel{
				LockFilePath: filepath.Join(t.TempDir(), "dbc.lock"),
				driverIndex: []dbc.Driver{
					{Path: "test-driver-1"},
					{Path: "test-driver-1", Registry: &dbc.Registry{}},
					registryA,
					registryBWithoutDriver,
				},
			}
			planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
				"test-driver-1": {Source: &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: tt.url}},
			}})
			require.NoError(t, err)
			assert.True(t, needsRegistry)
			items, err := model.createInstallList(planned)
			require.ErrorContains(t, err, "driver \"test-driver-1\" was not found in declared registry")
			assert.ErrorContains(t, err, tt.url)
			assert.Empty(t, items)
		})
	}
}

func TestCreateInstallListPreservesDefaultRegistryPrecedence(t *testing.T) {
	registryA := registryScopedTestDriver(t, "test-driver-1", "https://registry-a.example.test")
	registryB := registryScopedTestDriver(t, "test-driver-1", "https://registry-b.example.test")
	model := syncModel{
		LockFilePath: filepath.Join(t.TempDir(), "dbc.lock"),
		driverIndex:  []dbc.Driver{registryA, registryB},
	}
	planned, needsRegistry, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {},
	}})
	require.NoError(t, err)
	assert.True(t, needsRegistry)
	items, err := model.createInstallList(planned)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "https://registry-a.example.test", items[0].Release.Source.Reference)
}

func TestExplicitRegistryURLNormalizationAllowsOfflineLockedReplay(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "dbc.lock")
	entry := testRegistryLockEntryForPlatform(config.PlatformTuple())
	entry.Name = "test-driver-1"
	entry.Source.URL = "https://registry.example.test"
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))

	version, err := semver.NewConstraint("1.2.3")
	require.NoError(t, err)
	list := DriversList{Drivers: map[string]driverSpec{
		"test-driver-1": {
			Version: version,
			Source: &dbc.DriverSource{
				Type: dbc.DriverSourceRegistry,
				URL:  "HTTPS://REGISTRY.EXAMPLE.TEST/",
			},
		},
	}}
	model := syncModel{LockFilePath: lockPath}
	planned, needsRegistry, err := model.planSyncItems(list)
	require.NoError(t, err)
	assert.False(t, needsRegistry, "normalized explicit registry identity should permit offline lock replay")

	items, err := model.createInstallList(planned)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotNil(t, items[0].LockEntry)
	assert.Nil(t, model.driverIndex, "locked replay must not need registry discovery")
	assert.Equal(t, "https://registry.example.test", items[0].Release.Source.Reference)
	expected, _, err := expectedSyncPackageMetadata(items[0])
	require.NoError(t, err)
	assert.Equal(t, "https://registry.example.test", expected.SourceIdentity)
}
