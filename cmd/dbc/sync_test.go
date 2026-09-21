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
	"github.com/columnar-tech/dbc/internal/packslip"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/columnar-tech/dbc/internal/sourceresolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustTestInstallItem(t *testing.T, release resolution.ResolvedRelease, platform string, lockEntry *lockInfo) installItem {
	t.Helper()
	item, err := newInstallItem(release, 0, platform, lockEntry)
	require.NoError(t, err)
	return item
}

type syncPackslipResolverStub struct {
	release resolution.ResolvedRelease
	calls   int
	project string
	request packslip.Request
}

func (stub *syncPackslipResolverStub) Resolve(_ context.Context, source packslip.PackslipSource, request packslip.Request) (resolution.ResolvedRelease, error) {
	stub.calls++
	stub.project = source.Project
	stub.request = request
	return cloneResolvedReleaseForSync(stub.release), nil
}

func makeSyncPackageV2Archive(t *testing.T, path, id, version, platform string) ([]byte, string) {
	t.Helper()
	metadata := []byte(fmt.Sprintf(`package_version = 2
id = %q
name = "Test Driver"
version = %q
platform = %q

[Driver]
entrypoint = "TestDriverInit"

[Files]
driver = "driver.so"
`, id, version, platform))
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range []struct {
		name string
		data []byte
	}{{name: "dbc-package.toml", data: metadata}, {name: "driver.so", data: []byte("test library bytes")}} {
		require.NoError(t, tarWriter.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o600, Size: int64(len(entry.data)), Typeflag: tar.TypeReg}))
		_, err := tarWriter.Write(entry.data)
		require.NoError(t, err)
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	archiveBytes := archive.Bytes()
	require.NoError(t, os.WriteFile(path, archiveBytes, 0o600))
	digest, err := checksum(path)
	require.NoError(t, err)
	return archiveBytes, "sha256:" + digest
}

func makeSyncPackslipRelease(id, version, url, hash string, size int64) resolution.ResolvedRelease {
	primary := testTarget(config.PlatformTuple())
	secondary := testTarget(differentTestPlatformTuple(config.PlatformTuple()))
	otherSize := int64(9)
	return resolution.ResolvedRelease{
		DriverID: id,
		Version:  version,
		Source:   resolution.SourceSpec{Type: "packslip", Reference: "github.com/example/driver"},
		Evidence: []resolution.Evidence{{
			Kind:     resolution.EvidenceKindReleaseMetadata,
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://github.com/example/driver/releases/download/v1.2.3/packslip.sigstore.json"},
			Hash:     "sha256:" + strings.Repeat("a", 64),
		}},
		Artifacts: []resolution.Artifact{
			{Target: primary, Format: "tgz", PackageVersion: 2, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: url}, Hash: hash, Size: &size},
			{Target: secondary, Format: "tar.gz", PackageVersion: 2, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://assets.example.test/macos.tar.gz"}, Hash: "sha256:" + strings.Repeat("b", 64), Size: &otherSize},
		},
	}
}

func differentTestPlatformTuple(platform string) string {
	host, err := resolution.TargetFromPlatformTuple(platform)
	if err != nil {
		panic(err)
	}
	for _, candidate := range []string{"linux_amd64", "linux_arm64", "macos_amd64", "macos_arm64", "windows_amd64"} {
		target, err := resolution.TargetFromPlatformTuple(candidate)
		if err != nil {
			panic(err)
		}
		if target != host {
			return candidate
		}
	}
	panic("no alternate test platform is available")
}

func TestDifferentTestPlatformTupleAvoidsDuplicateTargets(t *testing.T) {
	for _, host := range []string{"linux_amd64", "linux_arm64", "macos_amd64", "macos_arm64", "windows_amd64"} {
		t.Run(host, func(t *testing.T) {
			got, err := resolution.TargetFromPlatformTuple(differentTestPlatformTuple(host))
			require.NoError(t, err)
			want, err := resolution.TargetFromPlatformTuple(host)
			require.NoError(t, err)
			assert.NotEqual(t, want, got, "secondary artifact target must differ from the current host")
		})
	}
}

func TestSyncProgressPercentTracksCompletedItems(t *testing.T) {
	if got := syncProgressPercent(0, 2); got != 0.5 {
		t.Fatalf("first completed item progress = %v, want 0.5", got)
	}
	if got := syncProgressPercent(1, 2); got != 1 {
		t.Fatalf("second completed item progress = %v, want 1", got)
	}
}

func TestPackageExecutorEnsureHookDoesNotRequireSyncWorker(t *testing.T) {
	item := mustTestInstallItem(t, resolution.ResolvedRelease{
		DriverID: "example",
		Version:  "1.2.3",
		Source:   resolution.SourceSpec{Type: "path", Reference: "./example.tgz"},
		Artifacts: []resolution.Artifact{{
			Target: testTarget(config.PlatformTuple()), Format: "tgz",
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "./example.tgz"},
		}},
	}, config.PlatformTuple(), nil)
	wantErr := errors.New("ensure hook invoked")
	calls := 0
	executor := newPackageExecutor(config.Config{}, t.TempDir(), true, nil, nil, nil,
		func(context.Context, config.Config, string, config.ExpectedPackageMetadata, config.InstallOptions, config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
			calls++
			return config.EnsurePackageResult{}, wantErr
		})
	_, err := executor.ensurePreparedPackage(context.Background(), &item)
	require.ErrorIs(t, err, wantErr)
	assert.Equal(t, 1, calls)
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
	defer closePreparedArchives(prepared.items)
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
	assert.Equal(t, 2, primaryArtifact.PackageVersion)
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
	defer closePreparedArchives(replayed.items)
	assert.Equal(t, 1, downloadCalls)
	assert.ElementsMatch(t, prepared.lock.Drivers[0].Artifacts, replayed.lock.Drivers[0].Artifacts)
}

func assertFileClosed(t *testing.T, file *os.File) {
	t.Helper()
	_, err := file.Stat()
	assert.Error(t, err, "stat on a closed file must fail")
}

func TestPackslipResolverMismatchIsRejectedBeforeCandidateLock(t *testing.T) {
	changes := []struct {
		name   string
		change func(*resolution.ResolvedRelease)
		want   string
	}{
		{name: "driver ID", change: func(release *resolution.ResolvedRelease) { release.DriverID = "other-driver" }, want: "requested driver"},
		{name: "source identity", change: func(release *resolution.ResolvedRelease) { release.Source.Reference = "github.com/example/other" }, want: "source identity"},
		{name: "version", change: func(release *resolution.ResolvedRelease) { release.Version = "1.2.4" }, want: "requested version"},
		{name: "package marker", change: func(release *resolution.ResolvedRelease) { release.Artifacts[0].PackageVersion = 0 }, want: "package_version 2"},
	}
	for _, test := range changes {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			base := makeSyncPackslipRelease("test-driver-1", "1.2.3", "https://assets.example.test/driver.tgz", "sha256:"+strings.Repeat("a", 64), 10)
			test.change(&base)
			resolver := &syncPackslipResolverStub{release: base}
			version, err := semver.NewConstraint("1.2.3")
			require.NoError(t, err)
			source := dbc.DriverSource{Type: dbc.DriverSourcePackslip, Project: "github.com/example/driver"}
			model := syncModel{
				baseModel:    baseModel{newPackslipResolver: func() (packslip.Resolver, error) { return resolver, nil }},
				LockFilePath: filepath.Join(dir, "dbc.lock"),
			}
			planned, _, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{
				"test-driver-1": {Version: version, Source: &source},
			}})
			require.NoError(t, err)
			_, err = model.createInstallListContext(context.Background(), planned)
			require.ErrorContains(t, err, test.want)
			assert.Equal(t, 1, resolver.calls)
			assert.NoFileExists(t, model.LockFilePath)
		})
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
	defer closePreparedArchives(prepared.items)
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
	model := syncModel{Path: filepath.Join(dir, "dbc.toml"), LockFilePath: filepath.Join(dir, "dbc.lock"), NoVerify: true}
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
	defer closePreparedArchives(prepared.items)
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
	model := syncModel{Path: filepath.Join(dir, "dbc.toml"), LockFilePath: filepath.Join(dir, "dbc.lock"), NoVerify: true}
	planned, _, err := model.planSyncItems(DriversList{Drivers: map[string]driverSpec{"test-driver-1": {Source: &source}}})
	require.NoError(t, err)
	items, err := model.createInstallListContext(context.Background(), planned)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(archivePath, append(archiveBytes, []byte("changed")...), 0o600))
	prepared, err := model.prepareInstallItems(context.Background(), items)
	defer closePreparedArchives(prepared.items)
	require.ErrorContains(t, err, "does not match expected hash")
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
	model := syncModel{Path: filepath.Join(dir, "dbc.toml"), LockFilePath: lockPath, NoVerify: true}
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
	defer closePreparedArchives(prepared.items)
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

func TestRegistryResolverResultsAreValidatedBeforeInstallItemCreation(t *testing.T) {
	constraint, err := semver.NewConstraint("=1.1.0")
	require.NoError(t, err)
	requirement, err := requirementForDriverSpec("test-driver-1", driverSpec{
		Version: constraint,
		Source:  &dbc.DriverSource{Type: dbc.DriverSourceRegistry, URL: testRegistry.BaseURL.String()},
	})
	require.NoError(t, err)
	base := resolution.ResolvedRelease{
		DriverID: "test-driver-1", Version: "1.1.0",
		Source: resolution.SourceSpec{Type: "registry", Reference: testRegistry.BaseURL.String()},
		Artifacts: []resolution.Artifact{{
			Target: testTarget(config.PlatformTuple()), Format: "tar.gz",
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://assets.example.test/driver.tar.gz"},
		}},
	}
	for _, test := range []struct {
		name   string
		change func(*resolution.ResolvedRelease)
		want   string
	}{
		{name: "driver", change: func(release *resolution.ResolvedRelease) { release.DriverID = "other" }, want: "driver ID"},
		{name: "source", change: func(release *resolution.ResolvedRelease) { release.Source.Reference = "https://other.example.test" }, want: "source"},
		{name: "version", change: func(release *resolution.ResolvedRelease) { release.Version = "9.9.9" }, want: "version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			release := cloneResolvedReleaseForSync(base)
			test.change(&release)
			item, err := installItemFromResolverResult(requirement, release, nil)
			require.ErrorContains(t, err, test.want)
			assert.Empty(t, item.Release.DriverID, "invalid resolver output cannot create an install candidate")
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

func TestPackageVersionIdentityDependsOnSource(t *testing.T) {
	locked := semver.MustParse("1.2.3+foo")
	resolvedSame := semver.MustParse("1.2.3+foo")
	resolvedDifferent := semver.MustParse("1.2.3+bar")

	assert.True(t, packageVersionsMatch("packslip", locked, resolvedSame))
	assert.False(t, packageVersionsMatch("packslip", locked, resolvedDifferent),
		"Packslip artifact identity includes build metadata")
	assert.True(t, packageVersionsMatch("path", locked, resolvedSame))
	assert.False(t, packageVersionsMatch("path", locked, resolvedDifferent),
		"path artifact identity includes build metadata")
	assert.True(t, packageVersionsMatch("registry", locked, resolvedDifferent),
		"registry keeps the existing SemVer equality semantics")
	assert.False(t, packageVersionsMatch("unknown", locked, resolvedSame),
		"unsupported source types have no release version identity rule")
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
}

func (suite *SubcommandTestSuite) TestSyncInstallFailureKeepsCompleteCandidateLock() {
	path := filepath.Join(suite.tempdir, "dbc.toml")
	lockPath := filepath.Join(suite.tempdir, "dbc.lock")
	suite.Require().NoError(os.WriteFile(path, []byte("[drivers]\n[drivers.test-driver-1]\n[drivers.test-driver-no-sig]\n"), 0644))
	var preparedPaths []string
	var downloadedDrivers []string
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
			return os.Open(copyPath)
		},
	}).(syncModel)
	model.worker.hooks.duringPrepare = func(_ context.Context, _ int, item installItem) error {
		preparedPaths = append(preparedPaths, item.Archive.File.Name())
		return nil
	}
	model.writeCandidateLock = func(path string, lock LockFile) error {
		if err := writeLockFileAtomic(path, lock); err != nil {
			return err
		}
		if len(preparedPaths) != 2 {
			return fmt.Errorf("expected two prepared archive snapshots, got %d", len(preparedPaths))
		}
		// Corrupt the final item only after the complete candidate is durable,
		// forcing execution to fail after one install while preserving the lock.
		return os.WriteFile(preparedPaths[len(preparedPaths)-1], []byte("broken after prepare"), 0600)
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
	setup := func(t *testing.T, currentPath, candidatePath string, proofData, candidateData []byte, candidateExists bool, currentEntrypoint, candidateEntrypoint string, registered bool) fixture {
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
		if registered {
			writeExternal(currentPath, proofData)
			if err := os.WriteFile(filepath.Join(filepath.Dir(currentPath), "LICENSE"), []byte("external sibling"), 0o644); err != nil {
				t.Fatal(err)
			}
			current := config.DriverInfo{
				ID: "test-driver-1", Name: "Legacy Shared Driver", Version: semver.MustParse("1.1.0"), Source: "dbc",
			}
			current.Driver.Entrypoint = currentEntrypoint
			current.Driver.Shared.Set(config.PlatformTuple(), currentPath)
			if err := config.CreateManifest(config.Config{Level: config.ConfigEnv, Location: root}, current); err != nil {
				t.Fatal(err)
			}
		}
		if candidateExists {
			if candidatePath != currentPath || !registered {
				writeExternal(candidatePath, candidateData)
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(candidatePath), "LICENSE"), []byte("candidate sibling"), 0o644); err != nil {
				t.Fatal(err)
			}
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
		archiveBytes := makeSyncManifestOnlyArchive(t, "1.1.0", candidatePath, candidateEntrypoint)
		if err := os.WriteFile(archivePath, archiveBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		return fixture{
			root: root, listPath: driverListPath, lockPath: lockPath,
			archivePath: archivePath, proofHash: proofHash, oldLock: []byte(legacyLock),
		}
	}

	run := func(t *testing.T, root, driverListPath, archivePath string, count *syncArchiveRunCounts, registry func() ([]dbc.Driver, error)) (string, error) {
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
		model.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, options config.InstallOptions, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
			result, err := config.EnsurePackage(ctx, cfg, driver, expected, options, callbacks)
			if result.Manifest != nil {
				count.install++
			}
			return result, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var output bytes.Buffer
		program := tea.NewProgram(model, tea.WithInput(nil), tea.WithOutput(&output),
			tea.WithoutRenderer(), tea.WithContext(ctx), tea.WithFilter(filterProgramMessage))
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
		fixture := setup(t, path, path, data, data, true, "DriverInit", "DriverInit", true)
		counts := &syncArchiveRunCounts{}
		_, err := run(t, fixture.root, fixture.listPath, fixture.archivePath, counts, getTestDriverRegistry)
		suite.NoError(err)
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
		_, err = run(t, fixture.root, fixture.listPath, fixture.archivePath, replayCounts, func() ([]dbc.Driver, error) {
			return nil, errors.New("locked v2 replay must not discover registry")
		})
		suite.NoError(err)
		suite.Equal(syncArchiveRunCounts{download: 1}, *replayCounts, "manifest-only locked replay validates its exact archive without registry discovery")
		lockAfterReplay, err := os.ReadFile(fixture.lockPath)
		suite.Require().NoError(err)
		suite.Equal(lockAfterMigration, lockAfterReplay)
	})

	for _, test := range []struct {
		name                 string
		candidatePathChanged bool
		candidateEntrypoint  string
	}{
		{name: "external path changed", candidatePathChanged: true, candidateEntrypoint: "DriverInit"},
		{name: "entrypoint changed", candidateEntrypoint: "OtherInit"},
	} {
		suite.Run(test.name, func() {
			t := suite.T()
			root := t.TempDir()
			currentPath := filepath.Join(root, "external-a", "library.so")
			candidatePath := currentPath
			if test.candidatePathChanged {
				candidatePath = filepath.Join(root, "external-b", "library.so")
			}
			data := []byte("same legacy external library")
			fixture := setup(t, currentPath, candidatePath, data, data, true, "DriverInit", test.candidateEntrypoint, true)
			before := map[string][]byte{}
			for _, external := range []string{currentPath, candidatePath} {
				for _, file := range []string{external, filepath.Join(filepath.Dir(external), "LICENSE")} {
					if _, ok := before[file]; ok {
						continue
					}
					var readErr error
					before[file], readErr = os.ReadFile(file)
					suite.Require().NoError(readErr)
				}
			}
			counts := &syncArchiveRunCounts{}
			_, err := run(t, fixture.root, fixture.listPath, fixture.archivePath, counts, getTestDriverRegistry)
			suite.NoError(err)
			suite.Equal(1, counts.install)
			installed, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: fixture.root}, "test-driver-1")
			suite.Require().NoError(err)
			suite.Equal(candidatePath, installed.Driver.Shared.Get(config.PlatformTuple()))
			suite.Equal(test.candidateEntrypoint, installed.Driver.Entrypoint)
			installedHash, err := checksum(candidatePath)
			suite.Require().NoError(err)
			suite.Equal(fixture.proofHash, installedHash)
			updated, err := loadLockFile(fixture.lockPath)
			suite.Require().NoError(err)
			suite.Require().NotNil(updated.lockinfo["test-driver-1"].Legacy)
			suite.Equal(fixture.proofHash, updated.lockinfo["test-driver-1"].Legacy.LibraryHash)
			for file, expected := range before {
				dataAfter, readErr := os.ReadFile(file)
				suite.NoError(readErr)
				suite.Equal(expected, dataAfter, "external files and siblings must remain untouched")
			}
		})
	}

	for _, test := range []struct {
		name           string
		candidateData  []byte
		candidateFound bool
	}{
		{name: "candidate bytes mismatch", candidateData: []byte("not the legacy library"), candidateFound: true},
		{name: "candidate file missing", candidateFound: false},
	} {
		suite.Run(test.name, func() {
			t := suite.T()
			root := t.TempDir()
			currentPath := filepath.Join(root, "external-current", "library.so")
			candidatePath := filepath.Join(root, "external-candidate", "library.so")
			proofData := []byte("preserved legacy library")
			fixture := setup(t, currentPath, candidatePath, proofData, test.candidateData, test.candidateFound, "DriverInit", "DriverInit", true)
			beforeManifest, err := os.ReadFile(filepath.Join(fixture.root, "test-driver-1.toml"))
			suite.Require().NoError(err)
			counts := &syncArchiveRunCounts{}
			out, err := run(t, fixture.root, fixture.listPath, fixture.archivePath, counts, getTestDriverRegistry)
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
			if test.candidateFound {
				candidateAfter, err := os.ReadFile(candidatePath)
				suite.Require().NoError(err)
				suite.Equal(test.candidateData, candidateAfter)
			} else {
				_, err := os.Stat(candidatePath)
				suite.ErrorIs(err, os.ErrNotExist)
			}
		})
	}

	for _, test := range []struct {
		name           string
		candidateData  []byte
		candidateFound bool
		wantInstall    int
	}{
		{name: "unregistered external proof installs", candidateData: []byte("unregistered legacy library"), candidateFound: true, wantInstall: 1},
		{name: "unregistered mismatched external proof fails", candidateData: []byte("wrong bytes"), candidateFound: true},
		{name: "unregistered missing external proof fails", candidateFound: false},
	} {
		suite.Run(test.name, func() {
			t := suite.T()
			root := t.TempDir()
			candidatePath := filepath.Join(root, "external", "library.so")
			proofData := []byte("unregistered legacy library")
			fixture := setup(t, "", candidatePath, proofData, test.candidateData, test.candidateFound, "", "DriverInit", false)
			counts := &syncArchiveRunCounts{}
			out, err := run(t, fixture.root, fixture.listPath, fixture.archivePath, counts, getTestDriverRegistry)
			if test.wantInstall == 0 {
				suite.Error(err)
				suite.Contains(out, "candidate package external library does not match the legacy lock proof")
				suite.Equal(0, counts.install)
				lockAfter, readErr := os.ReadFile(fixture.lockPath)
				suite.Require().NoError(readErr)
				suite.Equal(fixture.oldLock, lockAfter)
				_, driverErr := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: fixture.root}, "test-driver-1")
				suite.Error(driverErr)
			} else {
				suite.NoError(err)
				suite.Equal(1, counts.install)
				installed, driverErr := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: fixture.root}, "test-driver-1")
				suite.Require().NoError(driverErr)
				suite.Equal(candidatePath, installed.Driver.Shared.Get(config.PlatformTuple()))
				installedHash, hashErr := checksum(candidatePath)
				suite.Require().NoError(hashErr)
				suite.Equal(fixture.proofHash, installedHash)
				updated, loadErr := loadLockFile(fixture.lockPath)
				suite.Require().NoError(loadErr)
				suite.Require().NotNil(updated.lockinfo["test-driver-1"].Legacy)
			}
			suite.Equal(1, counts.registry)
			suite.Equal(1, counts.download)
			if test.candidateFound {
				candidateAfter, readErr := os.ReadFile(candidatePath)
				suite.Require().NoError(readErr)
				suite.Equal(test.candidateData, candidateAfter)
			}
		})
	}
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
	suite.Contains(suite.runCmdErr(v2Model), "package v2 requires archive hash and size metadata")
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

func (suite *SubcommandTestSuite) TestSyncSameVersionRequiresMatchingManagedReceipt() {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, string, config.InstallReceipt)
	}{
		{name: "healthy receipt skips", mutate: func(*testing.T, string, string, config.InstallReceipt) {}},
		{name: "missing receipt repairs", mutate: func(t *testing.T, _ string, receiptPath string, _ config.InstallReceipt) {
			if err := os.Remove(receiptPath); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "invalid receipt repairs", mutate: func(t *testing.T, _ string, receiptPath string, _ config.InstallReceipt) {
			if err := os.WriteFile(receiptPath, []byte("not json"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "driver id mismatch repairs", mutate: func(t *testing.T, _ string, receiptPath string, receipt config.InstallReceipt) {
			receipt.DriverID = "another-driver"
			writeSyncReceipt(t, receiptPath, receipt)
		}},
		{name: "version mismatch repairs", mutate: func(t *testing.T, _ string, receiptPath string, receipt config.InstallReceipt) {
			receipt.DriverVersion = "9.9.9"
			writeSyncReceipt(t, receiptPath, receipt)
		}},
		{name: "source type mismatch repairs", mutate: func(t *testing.T, _ string, receiptPath string, receipt config.InstallReceipt) {
			receipt.SourceType = "local"
			writeSyncReceipt(t, receiptPath, receipt)
		}},
		{name: "source identity mismatch repairs", mutate: func(t *testing.T, _ string, receiptPath string, receipt config.InstallReceipt) {
			receipt.SourceIdentity = "https://other.example"
			writeSyncReceipt(t, receiptPath, receipt)
		}},
		{name: "target mismatch repairs", mutate: func(t *testing.T, _ string, receiptPath string, receipt config.InstallReceipt) {
			if receipt.Platform == "windows_amd64" {
				receipt.Platform = "linux_amd64"
			} else {
				receipt.Platform = "windows_amd64"
			}
			writeSyncReceipt(t, receiptPath, receipt)
		}},
		{name: "archive hash mismatch repairs", mutate: func(t *testing.T, _ string, receiptPath string, receipt config.InstallReceipt) {
			receipt.ArchiveHash = "sha256:" + strings.Repeat("0", 64)
			writeSyncReceipt(t, receiptPath, receipt)
		}},
		{name: "archive size mismatch repairs", mutate: func(t *testing.T, _ string, receiptPath string, receipt config.InstallReceipt) {
			receipt.ArchiveSize++
			writeSyncReceipt(t, receiptPath, receipt)
		}},
		{name: "missing fingerprint repairs", mutate: func(t *testing.T, _ string, receiptPath string, receipt config.InstallReceipt) {
			receipt.RegistrationFingerprintAlgorithm = ""
			receipt.RegistrationFingerprintVersion = 0
			receipt.RegistrationFingerprint = ""
			writeSyncReceipt(t, receiptPath, receipt)
		}},
		{name: "unknown fingerprint repairs", mutate: func(t *testing.T, _ string, receiptPath string, receipt config.InstallReceipt) {
			receipt.RegistrationFingerprintVersion++
			writeSyncReceipt(t, receiptPath, receipt)
		}},
		{name: "fingerprint mismatch repairs", mutate: func(t *testing.T, _ string, receiptPath string, receipt config.InstallReceipt) {
			receipt.RegistrationFingerprint = "sha256:" + strings.Repeat("0", 64)
			writeSyncReceipt(t, receiptPath, receipt)
		}},
		{name: "runtime registration metadata mismatch repairs", mutate: func(t *testing.T, libraryPath, _ string, _ config.InstallReceipt) {
			root := filepath.Dir(filepath.Dir(libraryPath))
			cfg := config.Config{Level: config.ConfigEnv, Location: root}
			installed, err := config.GetDriver(cfg, "test-driver-1")
			if err != nil {
				t.Fatal(err)
			}
			installed.Name = "Tampered Runtime Name"
			if err := config.CreateManifest(cfg, installed); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "tampered library repairs", mutate: func(t *testing.T, libraryPath, _ string, _ config.InstallReceipt) {
			if err := os.WriteFile(libraryPath, []byte("tampered"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing library repairs", mutate: func(t *testing.T, libraryPath, _ string, _ config.InstallReceipt) {
			if err := os.Remove(libraryPath); err != nil {
				t.Fatal(err)
			}
		}},
	}

	for _, test := range tests {
		suite.Run(test.name, func() {
			t := suite.T()
			root := t.TempDir()
			t.Setenv("ADBC_DRIVER_PATH", root)
			driverListPath := filepath.Join(root, "dbc.toml")
			if err := os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			suite.runCmd(SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(testBaseModel()))

			cfg := config.Config{Level: config.ConfigEnv, Location: root}
			installed, err := config.GetDriver(cfg, "test-driver-1")
			if err != nil {
				t.Fatal(err)
			}
			libraryPath := installed.Driver.Shared.Get(config.PlatformTuple())
			receiptPath := filepath.Join(filepath.Dir(libraryPath), "dbc-install-receipt.json")
			receiptBytes, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal(err)
			}
			var receipt config.InstallReceipt
			if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, libraryPath, receiptPath, receipt)

			registryCalls, downloadCalls := 0, 0
			installCalls := 0
			model := SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(baseModel{
				getDriverRegistry: func() ([]dbc.Driver, error) {
					registryCalls++
					return nil, errors.New("locked replay must not discover registries")
				},
				downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
					downloadCalls++
					return downloadTestPkg(pkg)
				},
			}).(syncModel)
			model.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, options config.InstallOptions, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
				result, err := config.EnsurePackage(ctx, cfg, driver, expected, options, callbacks)
				if result.Manifest != nil {
					installCalls++
				}
				return result, err
			}
			suite.runCmd(model)
			suite.Equal(0, registryCalls)
			if strings.Contains(test.name, "healthy") {
				suite.Equal(0, downloadCalls)
				suite.Zero(installCalls)
			} else {
				suite.Equal(1, downloadCalls)
				suite.Equal(1, installCalls)
			}

			installed, err = config.GetDriver(cfg, "test-driver-1")
			if err != nil {
				t.Fatal(err)
			}
			libraryPath = installed.Driver.Shared.Get(config.PlatformTuple())
			receipt, managed, present, valid := config.InspectInstallReceipt(root, "test-driver-1", libraryPath)
			if !managed || !present || !valid || !config.VerifyInstallReceiptLibraryIntegrity(libraryPath, receipt) {
				t.Fatalf("repaired installation has invalid receipt/library: managed %v present %v valid %v", managed, present, valid)
			}
			if !config.InstallReceiptMatchesRuntimeRegistration(receipt, installed, config.PlatformTuple()) {
				t.Fatal("repaired receipt does not prove current runtime registration")
			}
		})
	}
}

func (suite *SubcommandTestSuite) TestSyncLockedPackageVersionMismatchDoesNotReuseReceiptOrMutateInstall() {
	t := suite.T()
	root := t.TempDir()
	t.Setenv("ADBC_DRIVER_PATH", root)
	driverListPath := filepath.Join(root, "dbc.toml")
	lockPath := filepath.Join(root, "dbc.lock")
	require.NoError(t, os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
	suite.runCmd(SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(testBaseModel()))

	cfg := config.Config{Level: config.ConfigEnv, Location: root}
	before, err := config.GetDriver(cfg, "test-driver-1")
	require.NoError(t, err)
	beforeLibrary := before.Driver.Shared.Get(config.PlatformTuple())
	lock, err := loadLockFile(lockPath)
	require.NoError(t, err)
	entry := lock.lockinfo["test-driver-1"]
	require.Len(t, entry.Artifacts, 1)
	entry.Artifacts[0].PackageVersion = 2
	lock.Drivers = []lockInfo{entry}
	require.NoError(t, writeLockFileAtomic(lockPath, lock))

	downloadCalls, installCalls := 0, 0
	model := SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			return nil, errors.New("exact locked replay must not discover registries")
		},
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloadCalls++
			return downloadTestPkg(pkg)
		},
	}).(syncModel)
	model.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, options config.InstallOptions, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		installCalls++
		return config.EnsurePackage(ctx, cfg, driver, expected, options, callbacks)
	}

	output := suite.runCmdErr(model)
	require.Contains(t, output, "dbc package version mismatch")
	require.Equal(t, 1, downloadCalls, "a receipt for another package format must not skip archive validation")
	require.Zero(t, installCalls, "package format validation must fail before package installation")
	after, err := config.GetDriver(cfg, "test-driver-1")
	require.NoError(t, err)
	assert.Equal(t, before.FilePath, after.FilePath)
	assert.Equal(t, beforeLibrary, after.Driver.Shared.Get(config.PlatformTuple()), "failed validation must leave the installed generation untouched")
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

func (suite *SubcommandTestSuite) TestSyncManifestOnlyInstallFailureConvergesFromCandidateLock() {
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
			return archive, nil
		},
	}).(syncModel)
	first.worker.hooks.duringPrepare = func(_ context.Context, _ int, item installItem) error {
		preparedArchivePath = item.Archive.File.Name()
		return nil
	}
	first.worker.hooks.beforeCandidateSave = func(context.Context) error {
		return os.WriteFile(preparedArchivePath, []byte("corrupted after validation"), 0o600)
	}
	firstOutput := suite.runCmdErr(first)
	suite.Contains(firstOutput, "archive hash mismatch")
	suite.Equal("test-driver-1", preparedPackage.Driver.Path)
	candidateLock, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	locked, err := loadLockFile(lockPath)
	suite.Require().NoError(err)
	artifact, err := selectLockedArtifact(locked.lockinfo["test-driver-1"], config.PlatformTuple(), false)
	suite.Require().NoError(err)
	suite.NotEmpty(artifact.Hash)
	suite.Require().NotNil(artifact.Size)
	suite.Equal(int64(len(archiveBytes)), *artifact.Size)
	_, err = config.GetDriver(config.Config{Level: config.ConfigEnv, Location: root}, "test-driver-1")
	suite.Error(err, "failed installation must not register the candidate")

	registryCalls, downloadCalls := 0, 0
	var replayPackage dbc.PkgInfo
	second := SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			registryCalls++
			return nil, errors.New("candidate lock replay must not discover the registry")
		},
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloadCalls++
			replayPackage = pkg
			return makeArchive()
		},
	})
	suite.runCmd(second)
	suite.Zero(registryCalls)
	suite.Equal(1, downloadCalls)
	suite.Equal(artifact.Location.Value, replayPackage.Path.String())
	suite.Equal(artifact.Hash, replayPackage.ArtifactHash)
	suite.Require().NotNil(replayPackage.ArtifactSize)
	suite.Equal(*artifact.Size, *replayPackage.ArtifactSize)
	convergedLock, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(candidateLock, convergedLock)
	installed, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: root}, "test-driver-1")
	suite.Require().NoError(err)
	suite.Equal(externalLibrary, installed.Driver.Shared.Get(config.PlatformTuple()))
	suite.Equal("DriverInit", installed.Driver.Entrypoint)
}

func (suite *SubcommandTestSuite) TestSyncPackslipInstallFailureConvergesFromCandidateLock() {
	root := suite.T().TempDir()
	suite.T().Setenv("ADBC_DRIVER_PATH", root)
	projectPath := filepath.Join(root, "dbc.toml")
	archivePath := filepath.Join(root, "driver.tgz")
	lockPath := filepath.Join(root, "dbc.lock")
	const version = "1.2.3+build.5"
	archiveBytes, archiveHash := makeSyncPackageV2Archive(suite.T(), archivePath, "test-driver-1", version, config.PlatformTuple())
	archiveSize := int64(len(archiveBytes))
	location := "https://assets.example.test/driver.tgz"
	release := makeSyncPackslipRelease("test-driver-1", version, location, archiveHash, archiveSize)
	resolver := &syncPackslipResolverStub{release: release}
	suite.Require().NoError(os.WriteFile(projectPath, []byte("[drivers.test-driver-1]\nversion = '"+version+"'\n[drivers.test-driver-1.source]\ntype = 'packslip'\nproject = 'github.com/example/driver'\n"), 0o600))

	var registryCalls, resolverCreations int
	first := SyncCmd{Path: projectPath, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			registryCalls++
			return nil, errors.New("Packslip sync must not discover registries")
		},
		newPackslipResolver: func() (packslip.Resolver, error) {
			resolverCreations++
			return resolver, nil
		},
		fetchPackslipArtifact: func(_ context.Context, artifactURL *url.URL) (io.ReadCloser, error) {
			suite.Equal(location, artifactURL.String())
			return os.Open(archivePath)
		},
	}).(syncModel)
	first.worker.hooks.ensurePackage = func(context.Context, config.Config, string, config.ExpectedPackageMetadata, config.InstallOptions, config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		return config.EnsurePackageResult{}, errors.New("injected install failure")
	}
	suite.Contains(suite.runCmdErr(first), "injected install failure")
	suite.Equal(0, registryCalls)
	suite.Equal(1, resolverCreations)
	candidateLock, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	locked, err := loadLockFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal("packslip", locked.lockinfo["test-driver-1"].Source.Type)
	suite.Equal(version, locked.lockinfo["test-driver-1"].Version.String())
	_, err = config.GetDriver(config.Config{Level: config.ConfigEnv, Location: root}, "test-driver-1")
	suite.Error(err, "an install failure after candidate save must not mutate runtime registration")

	var replayRegistryCalls, replayResolverCreations, replayDownloads int
	second := SyncCmd{Path: projectPath, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			replayRegistryCalls++
			return nil, errors.New("candidate lock replay must not discover registries")
		},
		newPackslipResolver: func() (packslip.Resolver, error) {
			replayResolverCreations++
			return nil, errors.New("candidate lock replay must not create a resolver")
		},
		fetchPackslipArtifact: func(_ context.Context, artifactURL *url.URL) (io.ReadCloser, error) {
			replayDownloads++
			suite.Equal(location, artifactURL.String())
			return os.Open(archivePath)
		},
	})
	suite.runCmd(second)
	suite.Zero(replayRegistryCalls)
	suite.Zero(replayResolverCreations)
	suite.Equal(1, replayDownloads, "replay opens the locked artifact without source discovery")
	convergedLock, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(candidateLock, convergedLock)
	installed, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: root}, "test-driver-1")
	suite.Require().NoError(err)
	suite.Equal(version, installed.Version.String())
}

func (suite *SubcommandTestSuite) TestSyncPathInstallFailureConvergesFromCandidateLock() {
	root := suite.T().TempDir()
	suite.T().Setenv("ADBC_DRIVER_PATH", root)
	projectPath := filepath.Join(root, "dbc.toml")
	archivePath := filepath.Join(root, "packages", "driver.tar.gz")
	lockPath := filepath.Join(root, "dbc.lock")
	suite.Require().NoError(os.MkdirAll(filepath.Dir(archivePath), 0o700))
	archiveBytes, err := os.ReadFile(filepath.Join("testdata", "test-driver-1.tar.gz"))
	suite.Require().NoError(err)
	suite.Require().NoError(os.WriteFile(archivePath, archiveBytes, 0o600))
	declaredPath := "./packages/driver.tar.gz"
	suite.Require().NoError(os.WriteFile(projectPath, []byte("[drivers.test-driver-1.source]\ntype = 'path'\npath = '"+declaredPath+"'\n"), 0o600))

	var registryCalls int
	newModel := func() syncModel {
		return SyncCmd{Path: projectPath, NoVerify: true}.GetModelCustom(baseModel{
			getDriverRegistry: func() ([]dbc.Driver, error) {
				registryCalls++
				return nil, errors.New("path sync must not discover registries")
			},
		}).(syncModel)
	}
	first := newModel()
	first.worker.hooks.ensurePackage = func(context.Context, config.Config, string, config.ExpectedPackageMetadata, config.InstallOptions, config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		return config.EnsurePackageResult{}, errors.New("injected install failure")
	}
	suite.Contains(suite.runCmdErr(first), "injected install failure")
	suite.Zero(registryCalls)
	candidateLock, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	locked, err := loadLockFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal("path", locked.lockinfo["test-driver-1"].Source.Type)
	suite.Equal(declaredPath, locked.lockinfo["test-driver-1"].Source.Path)
	suite.Equal(declaredPath, locked.lockinfo["test-driver-1"].Artifacts[0].Location.Value)
	suite.Equal("1.0.0", locked.lockinfo["test-driver-1"].Version.String())
	_, err = config.GetDriver(config.Config{Level: config.ConfigEnv, Location: root}, "test-driver-1")
	suite.Error(err, "an install failure after candidate save must not mutate runtime registration")

	second := newModel()
	suite.runCmd(second)
	suite.Zero(registryCalls)
	convergedLock, err := os.ReadFile(lockPath)
	suite.Require().NoError(err)
	suite.Equal(candidateLock, convergedLock)
	installed, err := config.GetDriver(config.Config{Level: config.ConfigEnv, Location: root}, "test-driver-1")
	suite.Require().NoError(err)
	suite.Equal("1.0.0", installed.Version.String())
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
	second.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, options config.InstallOptions, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		ensureCalls++
		result, err := config.EnsurePackage(ctx, cfg, driver, expected, options, callbacks)
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
	third.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, options config.InstallOptions, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		ensureCalls++
		return config.EnsurePackage(ctx, cfg, driver, expected, options, callbacks)
	}
	suite.Contains(suite.runCmdErr(third), "does not match expected hash")
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

func (suite *SubcommandTestSuite) TestSyncReceiptRepairUsesSelectedMultiPathRegistrationRoot() {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, string, config.InstallReceipt)
	}{
		{name: "receipt mismatch", mutate: func(t *testing.T, _ string, receiptPath string, receipt config.InstallReceipt) {
			receipt.SourceIdentity = "https://different.example"
			writeSyncReceipt(t, receiptPath, receipt)
		}},
		{name: "library tampering", mutate: func(t *testing.T, libraryPath, _ string, _ config.InstallReceipt) {
			if err := os.WriteFile(libraryPath, []byte("tampered"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		suite.Run(test.name, func() {
			t := suite.T()
			root := t.TempDir()
			first := filepath.Join(root, "first")
			second := filepath.Join(root, "second")
			suite.Require().NoError(os.MkdirAll(first, 0o755))
			suite.Require().NoError(os.MkdirAll(second, 0o755))
			t.Setenv("ADBC_DRIVER_PATH", first+string(filepath.ListSeparator)+second)
			driverListPath := filepath.Join(root, "dbc.toml")
			suite.Require().NoError(os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
			suite.runCmd(SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(testBaseModel()))

			firstManifest := filepath.Join(first, "test-driver-1.toml")
			suite.Require().NoError(os.Remove(firstManifest))
			archive, err := os.Open(filepath.Join("testdata", "test-driver-1.1.tar.gz"))
			suite.Require().NoError(err)
			_, err = config.InstallPackage(config.Config{Level: config.ConfigEnv, Location: second}, "test-driver-1", archive, config.ExpectedPackageMetadata{
				ID: "test-driver-1", Version: "1.1.0", Platform: config.PlatformTuple(),
				SourceType: "registry", SourceIdentity: testRegistry.BaseURL.String(),
			}, config.InstallOptions{})
			suite.Require().NoError(err)
			suite.NoError(archive.Close())

			combined := config.Config{Level: config.ConfigEnv, Location: first + string(filepath.ListSeparator) + second}
			installed, err := config.GetDriver(combined, "test-driver-1")
			suite.Require().NoError(err)
			suite.Equal(second, installed.FilePath)
			libraryPath := installed.Driver.Shared.Get(config.PlatformTuple())
			receiptPath := filepath.Join(filepath.Dir(libraryPath), "dbc-install-receipt.json")
			receiptBytes, err := os.ReadFile(receiptPath)
			suite.Require().NoError(err)
			var receipt config.InstallReceipt
			suite.Require().NoError(json.Unmarshal(receiptBytes, &receipt))
			test.mutate(t, libraryPath, receiptPath, receipt)

			downloadCalls, registryCalls := 0, 0
			model := SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(baseModel{
				getDriverRegistry: func() ([]dbc.Driver, error) {
					registryCalls++
					return nil, errors.New("exact lock replay must not discover registries")
				},
				downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
					downloadCalls++
					return downloadTestPkg(pkg)
				},
			})
			suite.runCmd(model)
			suite.Equal(0, registryCalls)
			suite.Equal(1, downloadCalls)

			installed, err = config.GetDriver(config.Config{Level: config.ConfigEnv, Location: first + string(filepath.ListSeparator) + second}, "test-driver-1")
			suite.Require().NoError(err)
			suite.Equal(first, installed.FilePath, "repair should publish to the configured primary environment path")
			finalLibrary := installed.Driver.Shared.Get(config.PlatformTuple())
			finalReceipt, managed, present, valid, err := config.InspectDriverInstallReceipt(config.Config{Level: config.ConfigEnv, Location: first + string(filepath.ListSeparator) + second}, installed)
			suite.Require().NoError(err)
			suite.True(managed)
			suite.True(present)
			suite.True(valid)
			suite.True(config.VerifyInstallReceiptLibraryIntegrity(finalLibrary, finalReceipt))
		})
	}
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
			model.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, options config.InstallOptions, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
				result, err := config.EnsurePackage(ctx, cfg, driver, expected, options, callbacks)
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

func (suite *SubcommandTestSuite) TestSyncRechecksPreparedReceiptBeforeSkipping() {
	root := suite.T().TempDir()
	suite.T().Setenv("ADBC_DRIVER_PATH", root)
	driverListPath := filepath.Join(root, "dbc.toml")
	suite.Require().NoError(os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
	suite.runCmd(SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(testBaseModel()))
	cfg := config.Config{Level: config.ConfigEnv, Location: root}
	before, err := config.GetDriver(cfg, "test-driver-1")
	suite.Require().NoError(err)
	beforeLibrary := before.Driver.Shared.Get(config.PlatformTuple())

	downloadCalls, installCalls := 0, 0
	model := SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			return nil, errors.New("exact lock replay must not discover registries")
		},
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloadCalls++
			return downloadTestPkg(pkg)
		},
	}).(syncModel)
	model.worker.hooks.duringPrepare = func(_ context.Context, _ int, _ installItem) error {
		current, err := config.GetDriver(cfg, "test-driver-1")
		if err != nil {
			return err
		}
		return config.UninstallDriver(cfg, current)
	}
	model.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, options config.InstallOptions, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		result, err := config.EnsurePackage(ctx, cfg, driver, expected, options, callbacks)
		if result.Manifest != nil {
			installCalls++
		}
		return result, err
	}
	suite.runCmd(model)
	suite.Equal(1, downloadCalls, "a prepared fast-skip hint that became stale must lazily fetch the exact locked artifact")
	suite.Equal(1, installCalls)
	after, err := config.GetDriver(cfg, "test-driver-1")
	suite.Require().NoError(err)
	suite.NotEqual(beforeLibrary, after.Driver.Shared.Get(config.PlatformTuple()))
}

func (suite *SubcommandTestSuite) TestSyncSkipsExactCandidateInstalledDuringArchiveProvider() {
	root := suite.T().TempDir()
	suite.T().Setenv("ADBC_DRIVER_PATH", root)
	driverListPath := filepath.Join(root, "dbc.toml")
	suite.Require().NoError(os.WriteFile(driverListPath, []byte("[drivers]\n[drivers.test-driver-1]\n"), 0o644))
	suite.runCmd(SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(testBaseModel()))
	cfg := config.Config{Level: config.ConfigEnv, Location: root}
	current, err := config.GetDriver(cfg, "test-driver-1")
	suite.Require().NoError(err)
	libraryPath := current.Driver.Shared.Get(config.PlatformTuple())
	receiptPath := filepath.Join(filepath.Dir(libraryPath), "dbc-install-receipt.json")
	data, err := os.ReadFile(receiptPath)
	suite.Require().NoError(err)
	var receipt config.InstallReceipt
	suite.Require().NoError(json.Unmarshal(data, &receipt))
	receipt.SourceIdentity = "https://stale.example"
	writeSyncReceipt(suite.T(), receiptPath, receipt)

	downloadCalls, directInstalls, ensureInstalls := 0, 0, 0
	var predicateCalls int
	model := SyncCmd{Path: driverListPath, NoVerify: true}.GetModelCustom(baseModel{
		getDriverRegistry: func() ([]dbc.Driver, error) {
			return nil, errors.New("exact lock replay must not discover registries")
		},
		downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
			downloadCalls++
			return downloadTestPkg(pkg)
		},
	}).(syncModel)
	model.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, options config.InstallOptions, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		currentMatches := callbacks.CurrentMatches
		callbacks.CurrentMatches = func(current *config.DriverInfo) (bool, error) {
			predicateCalls++
			return currentMatches(current)
		}
		archiveProvider := callbacks.Archive
		callbacks.Archive = func(ctx context.Context) (*os.File, error) {
			archive, err := archiveProvider(ctx)
			if err != nil {
				return nil, err
			}
			concurrentArchive, err := os.Open(archive.Name())
			if err != nil {
				return nil, err
			}
			_, installErr := config.InstallPackage(cfg, driver, concurrentArchive, expected, options)
			closeErr := concurrentArchive.Close()
			if installErr != nil {
				return nil, installErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			directInstalls++
			return archive, nil
		}
		result, err := config.EnsurePackage(ctx, cfg, driver, expected, options, callbacks)
		if result.Manifest != nil {
			ensureInstalls++
		}
		return result, err
	}
	run := startSyncProgram(model)
	resultModel := waitSyncProgram(suite.T(), run)
	suite.Require().NoError(resultModel.Err(), run.output.String())
	suite.Equal(1, downloadCalls)
	suite.Equal(1, directInstalls)
	suite.Equal(0, ensureInstalls, "phase two must skip after the provider's concurrent exact install")
	suite.Equal(2, predicateCalls, "phase one and phase two must both inspect current registration")
	after, err := config.GetDriver(cfg, "test-driver-1")
	suite.Require().NoError(err)
	finalReceipt, managed, present, valid, err := config.InspectDriverInstallReceipt(cfg, after)
	suite.Require().NoError(err)
	suite.True(managed)
	suite.True(present)
	suite.True(valid)
	suite.True(config.InstallReceiptMatchesRuntimeRegistration(finalReceipt, after, config.PlatformTuple()))
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
			var downloadedArchive, preparedArchive *os.File
			model := SyncCmd{Path: path, NoVerify: true, Json: stage != "prepare", JsonStreamProgress: stage == "prepare"}.GetModelCustom(baseModel{
				getDriverRegistry: func() ([]dbc.Driver, error) {
					if stage == "registry" {
						close(entered)
						<-release
					}
					return getTestDriverRegistry()
				},
				downloadPkg: func(pkg dbc.PkgInfo) (*os.File, error) {
					downloadedArchive, err = downloadTestPkg(pkg)
					if stage == "download" {
						close(entered)
						<-release
					}
					return downloadedArchive, err
				},
			}).(syncModel)
			model.worker.hooks.duringPrepare = func(_ context.Context, _ int, item installItem) error {
				preparedArchive = item.Archive.File
				return nil
			}
			block := func(context.Context) error {
				close(entered)
				<-release
				return nil
			}
			switch stage {
			case "prepare":
				model.worker.hooks.duringPrepare = func(ctx context.Context, _ int, item installItem) error {
					preparedArchive = item.Archive.File
					return block(ctx)
				}
			case "candidate-save":
				model.worker.hooks.beforeCandidateSave = block
			case "install":
				model.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, options config.InstallOptions, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
					archiveProvider := callbacks.Archive
					callbacks.Archive = func(ctx context.Context) (*os.File, error) {
						file, err := archiveProvider(ctx)
						if err != nil {
							return nil, err
						}
						preparedArchive = file
						close(entered)
						<-release
						return nil, ctx.Err()
					}
					return config.EnsurePackage(ctx, cfg, driver, expected, options, callbacks)
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
			switch stage {
			case "registry":
				suite.Nil(downloadedArchive)
			case "download":
				_, statErr = downloadedArchive.Stat()
				suite.NoError(statErr, "source response stays open until the interrupted snapshot completes")
			default:
				_, statErr = preparedArchive.Stat()
				suite.NoError(statErr, "prepared snapshot stays open until the worker exits")
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
			if preparedArchive != nil {
				assertFileClosed(suite.T(), preparedArchive)
			}
			if stage == "download" {
				assertFileClosed(suite.T(), downloadedArchive)
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
	assertFileClosed(suite.T(), archive)
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
	second.worker.hooks.ensurePackage = func(ctx context.Context, cfg config.Config, driver string, expected config.ExpectedPackageMetadata, options config.InstallOptions, callbacks config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
		result, err := config.EnsurePackage(ctx, cfg, driver, expected, options, callbacks)
		if result.Manifest != nil {
			secondInstalls.Add(1)
		}
		return result, err
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
					message = installedDrvMsg{info: info, item: installItem{Platform: config.PlatformTuple()}}
					code = "checksum_failed"
				case "checksum-mismatch":
					libraryPath := filepath.Join(suite.T().TempDir(), "driver.so")
					suite.Require().NoError(os.WriteFile(libraryPath, []byte("installed library"), 0600))
					info := config.DriverInfo{ID: "example", Version: semver.MustParse("1.0.0")}
					info.Driver.Shared.Set(config.PlatformTuple(), libraryPath)
					item := installItem{Platform: config.PlatformTuple(), InstalledLibraryHash: strings.Repeat("0", 64)}
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
