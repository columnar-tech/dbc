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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLockArtifact(platform, digest string, size int64) lockArtifact {
	return lockArtifact{
		Platform: platform,
		URL:      "https://example.test/" + platform + ".tar.gz",
		Hash:     "sha256:" + strings.Repeat(digest, 64),
		Size:     &size,
		Format:   "tar.gz",
	}
}

func testLockEntry() lockInfo {
	version := semver.MustParse("1.2.3")
	return lockInfo{
		Name:    "example",
		Version: version,
		Source:  lockSource{Type: "packslip", Project: "github.com/example/driver"},
		Evidence: lockEvidence{
			BundleURL:  "https://example.test/release.packslip",
			BundleHash: "sha256:" + strings.Repeat("e", 64),
		},
		Artifacts: []lockArtifact{
			{
				Platform: "linux_amd64", OS: "linux", Arch: "amd64", LibC: "gnu", Variant: "v1", Format: "tar.gz",
				URL: "https://example.test/linux.tar.gz", Hash: "sha256:" + strings.Repeat("a", 64), Size: int64Pointer(128),
				HostRequirements: lockHostRequirements{
					OSMin: "3.2.0", GLibCMin: "2.17", Libs: []string{"libssl.so.3", "libc.so.6"},
					Bins: []lockNamedRequirement{{Name: "git", Min: "2.40"}},
				},
			},
			{
				Platform: "macos_arm64", OS: "macos", Arch: "arm64", Format: "tar.gz",
				URL: "https://example.test/macos.tar.gz", Hash: "sha256:" + strings.Repeat("b", 64), Size: int64Pointer(256),
			},
		},
	}
}

func int64Pointer(value int64) *int64 { return &value }

func TestLockFileV2DeterministicRoundTripPreservesArtifactMetadata(t *testing.T) {
	dir := t.TempDir()
	entry := testLockEntry()
	lock := LockFile{Version: lockFileVersion, Revision: 0, Drivers: []lockInfo{entry, {
		Name: "local", Version: semver.MustParse("0.1.0"),
		Source: lockSource{Type: "path", Path: "./packages/local.tar.gz"},
		Artifacts: []lockArtifact{{
			Platform: "windows_amd64", Path: "./packages/local.tar.gz", Hash: "sha256:" + strings.Repeat("c", 64), Size: int64Pointer(0), Format: "tar.gz",
		}},
	}}}
	firstPath := filepath.Join(dir, "first.lock")
	require.NoError(t, writeLockFileAtomic(firstPath, lock))
	first, err := os.ReadFile(firstPath)
	require.NoError(t, err)

	lock.Drivers[0].Artifacts[0], lock.Drivers[0].Artifacts[1] = lock.Drivers[0].Artifacts[1], lock.Drivers[0].Artifacts[0]
	secondPath := filepath.Join(dir, "second.lock")
	require.NoError(t, writeLockFileAtomic(secondPath, lock))
	second, err := os.ReadFile(secondPath)
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second), "serialization should be canonical regardless of input order")

	loaded, err := loadLockFile(firstPath)
	require.NoError(t, err)
	assert.Equal(t, lockFileVersion, loaded.Version)
	assert.Len(t, loaded.Drivers, 2)
	linux := loaded.lockinfo["example"].Artifacts[0]
	assert.Equal(t, "gnu", linux.LibC)
	assert.Equal(t, "v1", linux.Variant)
	assert.Equal(t, []string{"libc.so.6", "libssl.so.3"}, linux.HostRequirements.Libs)
	assert.Equal(t, "2.17", linux.HostRequirements.GLibCMin)
	assert.Equal(t, []lockNamedRequirement{{Name: "git", Min: "2.40"}}, linux.HostRequirements.Bins)
	assert.Equal(t, "./packages/local.tar.gz", loaded.lockinfo["local"].Artifacts[0].Path)
	assert.Zero(t, *loaded.lockinfo["local"].Artifacts[0].Size, "an explicit zero size is distinct from an omitted size")
	localRelease := loaded.lockinfo["local"].resolvedRelease()
	localRoundTrip, err := lockInfoFromResolvedRelease("local", localRelease)
	require.NoError(t, err)
	assert.Equal(t, "./packages/local.tar.gz", localRoundTrip.Artifacts[0].Path)
}

func TestLockFileV2AllowsPartialArtifactSetAndReplayNeedsNoDiscovery(t *testing.T) {
	entry := testLockEntry()
	entry.Artifacts = entry.Artifacts[:1]
	artifact, err := selectLockedArtifact(entry, "linux_amd64_v1", false)
	require.NoError(t, err)
	assert.Equal(t, entry.Artifacts[0].Hash, artifact.Hash)
	assert.Equal(t, entry.Artifacts[0].HostRequirements, artifact.HostRequirements)

	_, err = selectLockedArtifact(entry, "macos_arm64", false)
	var refreshErr *LockRefreshRequiredError
	assert.ErrorAs(t, err, &refreshErr)
	assert.ErrorIs(t, err, ErrLockRefreshRequired)
	assert.NotErrorIs(t, err, ErrLockedModeArtifactMissing)

	_, err = selectLockedArtifact(entry, "macos_arm64", true)
	assert.ErrorIs(t, err, ErrLockedArtifactMissing)
	assert.ErrorIs(t, err, ErrLockedModeArtifactMissing)
	assert.NotErrorIs(t, err, ErrLockRefreshRequired)
	assert.Contains(t, err.Error(), "locked mode")
}

func TestLockFileV2RejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbc.lock")
	require.NoError(t, os.WriteFile(path, []byte("version = 99\n"), 0o600))
	_, err := loadLockFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported lock file version 99")
}

func TestLockFileV2UsesTypedSourceFieldsAndRequiresEvidencePairs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbc.lock")
	require.NoError(t, writeLockFileAtomic(path, LockFile{Version: 2, Drivers: []lockInfo{testLockEntry()}}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "project = 'github.com/example/driver'")
	assert.NotContains(t, string(data), "reference =")
	assert.Contains(t, string(data), "revision = 0")

	entry := testLockEntry()
	entry.Evidence.BundleHash = ""
	err = validateLockInfo(entry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both bundle URL and hash")
}

func TestLockFileV1MigrationPreservesLibraryProofAndRequiresSamePlatformVerification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbc.lock")
	legacyHash := strings.Repeat("d", 64)
	body := "version = 1\n\n[[drivers]]\nname = \"example\"\nversion = \"1.2.3\"\nplatform = \"macos_arm64\"\nchecksum = \"" + legacyHash + "\"\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	old, err := loadLockFile(path)
	require.NoError(t, err)
	require.Equal(t, lockFileVersionV1, old.Version)
	require.NotNil(t, old.lockinfo["example"].Legacy)

	release := testResolvedRelease()
	_, err = migrateV1Entry(old.lockinfo["example"], release, "macos_arm64", nil)
	require.Error(t, err, "same-platform migration must require installed-library proof")

	verified := &VerifiedLegacyLibrary{Platform: "macos_arm64", LibraryHash: legacyHash}
	migrated, err := migrateV1Entry(old.lockinfo["example"], release, "macos_arm64", verified)
	require.NoError(t, err)
	require.NotNil(t, migrated.Legacy)
	assert.Equal(t, legacyHash, migrated.Legacy.LibraryHash)
	assert.NotEqual(t, "sha256:"+legacyHash, migrated.Artifacts[0].Hash, "v1 library checksum must not become an archive hash")
	assert.NoError(t, verifyLegacyLibraryProof(migrated, "macos_arm64", legacyHash))
	assert.Error(t, verifyLegacyLibraryProof(migrated, "macos_arm64", strings.Repeat("0", 64)))
	v2Path := filepath.Join(t.TempDir(), "migrated.lock")
	require.NoError(t, writeLockFileAtomic(v2Path, LockFile{Version: 2, Drivers: []lockInfo{migrated}}))
	reloaded, err := loadLockFile(v2Path)
	require.NoError(t, err)
	require.NotNil(t, reloaded.lockinfo["example"].Legacy)
	assert.Equal(t, legacyHash, reloaded.lockinfo["example"].Legacy.LibraryHash)

	otherPlatform, err := migrateV1Entry(old.lockinfo["example"], release, "linux_amd64", nil)
	require.NoError(t, err, "migration on another platform may retain but not apply the old proof")
	require.NotNil(t, otherPlatform.Legacy)
	assert.Equal(t, "macos_arm64", otherPlatform.Legacy.Platform)
}

func TestAtomicLockWriterKeepsOldFileWhenReplacementFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbc.lock")
	old := []byte("old lock contents\n")
	require.NoError(t, os.WriteFile(path, old, 0o600))
	err := writeLockFileAtomic(path, LockFile{Version: 2, Drivers: []lockInfo{{Name: "missing source"}}})
	require.Error(t, err)
	actual, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, old, actual)
}

func TestRefreshRejectsArtifactContradictionAndAllowsNewTarget(t *testing.T) {
	existing := testLockEntry()
	refreshed := existing
	contradictory := existing.Artifacts[0]
	contradictory.Hash = "sha256:" + strings.Repeat("f", 64)
	contradictory.Size = int64Pointer(999)
	refreshed.Artifacts = []lockArtifact{contradictory, testLockArtifact("windows_amd64", "c", 22)}
	_, err := refreshLockEntry(existing, refreshed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contradicts")

	refreshed.Artifacts = []lockArtifact{existing.Artifacts[0], testLockArtifact("windows_amd64", "c", 22)}
	merged, err := refreshLockEntry(existing, refreshed)
	require.NoError(t, err)
	assert.Len(t, merged.Artifacts, 3)
}

func TestVersionUpgradeIsSeparateFromMetadataRefresh(t *testing.T) {
	old := testLockEntry()
	old.Legacy = &legacyLibraryProof{Platform: "macos_arm64", LibraryHash: strings.Repeat("d", 64)}
	newVersion := testLockEntry()
	newVersion.Version = semver.MustParse("1.3.0")

	upgraded, err := upgradeLockEntry(old, newVersion)
	require.NoError(t, err)
	assert.Nil(t, upgraded.Legacy, "installed-library proofs must not cross release versions")

	_, err = upgradeLockEntry(old, old)
	require.Error(t, err)
	newVersion.Legacy = old.Legacy
	_, err = upgradeLockEntry(old, newVersion)
	require.Error(t, err)
}

func TestLockSnapshotRequiresFinalizedArtifactMetadata(t *testing.T) {
	release := testResolvedRelease()
	release.Artifacts[0].Hash = ""
	_, err := lockInfoFromResolvedRelease("example", release)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no finalized hash")

	release = testResolvedRelease()
	release.Artifacts = append(release.Artifacts, release.Artifacts[0])
	_, err = lockInfoFromResolvedRelease("example", release)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate artifact identity")
}

func TestSyncAdapterReusesCompleteV2SnapshotWithoutRegistryHashes(t *testing.T) {
	platform := config.PlatformTuple()
	osName, arch, libc, variant, ok := parsePlatformTuple(platform)
	require.True(t, ok)
	registryURL, err := url.Parse("https://registry.example.test")
	require.NoError(t, err)
	entry := lockInfo{
		Name:    "example",
		Version: semver.MustParse("1.2.3"),
		Source:  lockSource{Type: "registry", URL: registryURL.String()},
		Artifacts: []lockArtifact{{
			Platform: platform, OS: osName, Arch: arch, LibC: libc, Variant: variant,
			URL: "https://assets.example.test/archive.tar.gz", Hash: "sha256:" + strings.Repeat("a", 64), Size: int64Pointer(10), Format: "tar.gz",
		}},
	}
	item, err := installItemFromLockedArtifact("example", entry, entry.Artifacts[0])
	require.NoError(t, err)
	assert.Equal(t, registryURL.String(), item.Driver.Registry.BaseURL.String())
	assert.Equal(t, "https://assets.example.test/archive.tar.gz", item.Package.Path.String(), "artifact host is independent of source identity")
	assert.Equal(t, entry.Artifacts[0].Hash, item.Package.ArtifactHash)
	assert.Equal(t, *entry.Artifacts[0].Size, *item.Package.ArtifactSize)
	item.InstalledLibraryHash = strings.Repeat("f", 64)

	updated, err := lockEntryForItem(item)
	require.NoError(t, err)
	assert.Equal(t, entry, updated, "the existing archive snapshot must not require optional registry hash/size")
	assert.Empty(t, updated.legacyChecksumFor(platform), "archive hash is not an installed-library checksum")

	path := filepath.Join(t.TempDir(), "dbc.lock")
	require.NoError(t, writeLockFileAtomic(path, LockFile{Version: 2, Drivers: []lockInfo{updated}}))
	reloaded, err := loadLockFile(path)
	require.NoError(t, err)
	assert.Equal(t, entry.Artifacts[0].Hash, reloaded.lockinfo["example"].Artifacts[0].Hash,
		"post-install lock rewrite must keep the archive snapshot rather than the installed-library hash")
}

func TestSyncAdapterRejectsFreshEntryWithoutArchiveMetadata(t *testing.T) {
	registryURL, err := url.Parse("https://registry.example.test")
	require.NoError(t, err)
	packageURL, err := url.Parse("https://registry.example.test/archive.tar.gz")
	require.NoError(t, err)
	item := installItem{
		Driver:  dbc.Driver{Path: "example", Registry: &dbc.Registry{BaseURL: registryURL}},
		Package: dbc.PkgInfo{Version: semver.MustParse("1.2.3"), PlatformTuple: "linux_amd64", Path: packageURL},
	}
	_, err = lockEntryForItem(item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no finalized hash")
}

func TestSyncAdapterVerifiesLegacyLibraryProofBeforeReusingV2Entry(t *testing.T) {
	platform := config.PlatformTuple()
	osName, arch, libc, variant, ok := parsePlatformTuple(platform)
	require.True(t, ok)
	registryURL, err := url.Parse("https://registry.example.test")
	require.NoError(t, err)
	packageURL, err := url.Parse("https://registry.example.test/archive.tar.gz")
	require.NoError(t, err)
	entry := lockInfo{
		Name:    "example",
		Version: semver.MustParse("1.2.3"),
		Source:  lockSource{Type: "registry", URL: registryURL.String()},
		Legacy:  &legacyLibraryProof{Platform: platform, LibraryHash: strings.Repeat("d", 64)},
		Artifacts: []lockArtifact{{
			Platform: platform, OS: osName, Arch: arch, LibC: libc, Variant: variant,
			URL: "https://registry.example.test/archive.tar.gz", Hash: "sha256:" + strings.Repeat("a", 64), Size: int64Pointer(10),
		}},
	}
	item := installItem{
		Driver:               dbc.Driver{Path: "example", Registry: &dbc.Registry{BaseURL: registryURL}},
		Package:              dbc.PkgInfo{Version: entry.Version, PlatformTuple: platform, Path: packageURL},
		InstalledLibraryHash: strings.Repeat("0", 64), LockEntry: &entry,
	}
	_, err = lockEntryForItem(item)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "legacy installed-library checksum mismatch")
}

func TestDownloadedArchiveSnapshotUsesArchiveBytes(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "package.tar.gz")
	archiveBytes := []byte("compressed package fixture")
	require.NoError(t, os.WriteFile(archivePath, archiveBytes, 0o600))
	archive, err := os.Open(archivePath)
	require.NoError(t, err)
	defer archive.Close()

	item := installItem{Package: dbc.PkgInfo{}}
	require.NoError(t, snapshotDownloadedArchive(&item, archive))
	digest := sha256.Sum256(archiveBytes)
	assert.Equal(t, "sha256:"+hex.EncodeToString(digest[:]), item.ArchiveHash)
	assert.EqualValues(t, len(archiveBytes), item.ArchiveSize)
	assert.Empty(t, item.InstalledLibraryHash, "archive bytes must not be recorded as an installed-library hash")
}

func TestLockedArchiveDownloadMustMatchExpectedHashAndSize(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "package.tar.gz")
	archiveBytes := []byte("downloaded bytes differ from the lock")
	require.NoError(t, os.WriteFile(archivePath, archiveBytes, 0o600))
	digest := sha256.Sum256(archiveBytes)
	actualHash := "sha256:" + hex.EncodeToString(digest[:])
	actualSize := int64(len(archiveBytes))

	tests := []struct {
		name string
		pkg  dbc.PkgInfo
		want string
	}{
		{
			name: "hash mismatch",
			pkg:  dbc.PkgInfo{ArtifactHash: "sha256:" + strings.Repeat("a", 64), ArtifactSize: &actualSize},
			want: "does not match expected hash",
		},
		{
			name: "size mismatch",
			pkg:  dbc.PkgInfo{ArtifactHash: actualHash, ArtifactSize: int64Pointer(actualSize + 1)},
			want: "does not match expected size",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive, err := os.Open(archivePath)
			require.NoError(t, err)
			defer archive.Close()
			item := installItem{Package: tt.pkg}
			err = snapshotDownloadedArchive(&item, archive)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Empty(t, item.ArchiveHash, "failed verification must not produce a lock snapshot")
		})
	}
}

func TestV2RegistryReplaySkipsDiscoveryAndUsesLockedURLAndMetadata(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "dbc.toml")
	lockPath := filepath.Join(filepath.Dir(projectPath), "dbc.lock")
	entry := testRegistryLockEntryForPlatform(config.PlatformTuple())
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: 2, Drivers: []lockInfo{entry}}))
	require.NoError(t, os.WriteFile(projectPath, []byte("[drivers]\n[drivers.example]\n"), 0o600))

	discoveryCalls := 0
	model := syncModel{
		baseModel: baseModel{getDriverRegistry: func() ([]dbc.Driver, error) {
			discoveryCalls++
			return nil, errors.New("registry unavailable")
		}},
		Path: projectPath,
		// Deliberately include a changed/incomplete registry result. A valid v2
		// target artifact must remain authoritative and never consult it.
		driverIndex: []dbc.Driver{{Path: "example", Title: "changed registry metadata"}},
	}
	updated, cmd := model.Update(driversListMsg{path: projectPath, list: DriversList{
		Drivers: map[string]driverSpec{"example": {}},
	}})
	require.NotNil(t, cmd)
	msg := cmd()
	items, ok := msg.([]installItem)
	require.True(t, ok, "expected lock replay install items, got %T", msg)
	require.Len(t, items, 1)
	assert.Zero(t, discoveryCalls, "registry discovery must not run for a complete v2 registry artifact")
	assert.Equal(t, entry.Artifacts[0].URL, items[0].Package.Path.String())
	assert.Equal(t, entry.Artifacts[0].Hash, items[0].Package.ArtifactHash)
	assert.Equal(t, *entry.Artifacts[0].Size, *items[0].Package.ArtifactSize)
	assert.Equal(t, entry.Source.URL, items[0].Driver.Registry.BaseURL.String())

	// The normal completion path builds the entry from the locked PkgInfo and
	// rewrites v2 without changing its artifact snapshot.
	updatedModel := updated.(syncModel)
	updatedModel.LockFilePath = lockPath
	rewritten, err := lockEntryForItem(items[0])
	require.NoError(t, err)
	updatedModel.locked = LockFile{Version: 2, Drivers: []lockInfo{rewritten}}
	require.NoError(t, updatedModel.writeLockFile())
	reloaded, err := loadLockFile(lockPath)
	require.NoError(t, err)
	assert.Equal(t, entry.Artifacts[0], reloaded.lockinfo["example"].Artifacts[0])
}

func testRegistryLockEntryForPlatform(platform string) lockInfo {
	osName, arch, libc, variant, ok := parsePlatformTuple(platform)
	if !ok {
		panic("invalid test platform tuple: " + platform)
	}
	return lockInfo{
		Name:    "example",
		Version: semver.MustParse("1.2.3"),
		Source:  lockSource{Type: "registry", URL: "https://registry.example.test"},
		Artifacts: []lockArtifact{{
			Platform: platform, OS: osName, Arch: arch, LibC: libc, Variant: variant,
			Format: "tar.gz", URL: "https://assets.example.test/" + platform + ".tar.gz",
			Hash: "sha256:" + strings.Repeat("a", 64), Size: int64Pointer(10),
		}},
	}
}

func TestV2RegistryReplayFallsBackToDiscoveryOnlyWhenTargetArtifactIsMissing(t *testing.T) {
	tmp := t.TempDir()
	projectPath := filepath.Join(tmp, "dbc.toml")
	lockPath := filepath.Join(tmp, "dbc.lock")
	otherPlatform := "linux_amd64"
	if config.PlatformTuple() == otherPlatform {
		otherPlatform = "macos_arm64"
	}
	entry := testRegistryLockEntryForPlatform(otherPlatform)
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: 2, Drivers: []lockInfo{entry}}))
	require.NoError(t, os.WriteFile(projectPath, []byte("[drivers]\n[drivers.example]\n"), 0o600))

	discoveryCalls := 0
	model := syncModel{
		baseModel: baseModel{getDriverRegistry: func() ([]dbc.Driver, error) {
			discoveryCalls++
			return []dbc.Driver{}, nil
		}},
		Path: projectPath,
	}
	_, cmd := model.Update(driversListMsg{path: projectPath, list: DriversList{
		Drivers: map[string]driverSpec{"example": {}},
	}})
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(driversWithRegistryError)
	require.True(t, ok, "missing target should enter the existing registry fallback, got %T", msg)
	assert.Equal(t, 1, discoveryCalls)
}

func TestLockReplayRejectsMuslForGenericLinuxTargetAndAmbiguousArtifacts(t *testing.T) {
	entry := testLockEntry()
	entry.Artifacts = []lockArtifact{{
		Platform: "linux_amd64", LibC: "musl", URL: "https://example.test/musl.tar.gz",
		Hash: "sha256:" + strings.Repeat("a", 64), Size: int64Pointer(10),
	}}
	_, err := selectLockedArtifact(entry, "linux_amd64", false)
	assert.Error(t, err, "a musl build must not match generic linux_amd64")

	entry.Artifacts = []lockArtifact{testLockArtifact("linux_amd64", "a", 10), testLockArtifact("linux_amd64", "b", 11)}
	_, err = selectLockedArtifact(entry, "linux_amd64", false)
	assert.ErrorIs(t, err, ErrArtifactAmbiguous)

	duplicateSelector := testLockEntry().Artifacts[0]
	otherFormat := duplicateSelector
	otherFormat.Format = "zip"
	assert.Error(t, validateLockArtifacts([]lockArtifact{duplicateSelector, otherFormat}),
		"same target selector with different format must be rejected as ambiguous")
}

func TestLockResolvedReleaseRoundTripPreservesEvidenceAndSelectors(t *testing.T) {
	release := testResolvedRelease()
	entry, err := lockInfoFromResolvedRelease("example", release)
	require.NoError(t, err)
	got := entry.resolvedRelease()
	assert.Equal(t, release, got)
}

func testResolvedRelease() resolution.ResolvedRelease {
	size := int64(10)
	return resolution.ResolvedRelease{
		DriverID: "example",
		Version:  "1.2.3",
		Source:   resolution.SourceSpec{Type: "packslip", Reference: "github.com/example/driver"},
		Evidence: resolution.Evidence{
			BundleURL: "https://example.test/bundle", BundleHash: "sha256:" + strings.Repeat("e", 64),
		},
		Artifacts: []resolution.Artifact{{
			Platform: "linux_amd64", OS: "linux", Arch: "amd64", LibC: "gnu", Variant: "v1", Format: "tar.gz",
			URL: "https://example.test/linux.tar.gz", Hash: "sha256:" + strings.Repeat("a", 64), Size: &size,
			HostRequirements: resolution.HostRequirements{
				GLibCMin: "2.17", Libs: []string{"libc.so.6"},
				Bins: []resolution.NamedRequirement{{Name: "git", Min: "2.40"}},
			},
		}},
	}
}
