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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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
		Target:   testTarget(platform),
		Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/" + platform + ".tar.gz"},
		Hash:     "sha256:" + strings.Repeat(digest, 64),
		Size:     &size,
		Format:   "tar.gz",
	}
}

func testTarget(platform string) resolution.Target {
	target, err := resolution.TargetFromPlatformTuple(platform)
	if err != nil {
		panic(err)
	}
	return target
}

func testLockEntry() lockInfo {
	version := semver.MustParse("1.2.3")
	return lockInfo{
		Name:    "example",
		Version: version,
		Source:  lockSource{Type: "packslip", Project: "github.com/example/driver"},
		Evidence: []lockEvidence{{
			Kind:     resolution.EvidenceKindReleaseMetadata,
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/release.packslip"},
			Hash:     "sha256:" + strings.Repeat("e", 64),
		}},
		Artifacts: []lockArtifact{
			{
				Target: resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu", Variant: "v1"}, Format: "tar.gz", PackageVersion: 2,
				Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/linux.tar.gz"}, Hash: "sha256:" + strings.Repeat("a", 64), Size: int64Pointer(128),
				HostRequirements: lockHostRequirements{
					OSMin: "3.2.0", GLibCMin: "2.17", Libs: []string{"libssl.so.3", "libc.so.6"},
					Bins: []lockNamedRequirement{{Name: "git", Min: "2.40"}},
				},
			},
			{
				Target: resolution.Target{OS: "macos", Arch: "arm64"}, Format: "tar.gz", PackageVersion: 2,
				Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/macos.tar.gz"}, Hash: "sha256:" + strings.Repeat("b", 64), Size: int64Pointer(256),
			},
		},
	}
}

func int64Pointer(value int64) *int64 { return &value }

func TestLockFileV2DeterministicRoundTripPreservesArtifactMetadata(t *testing.T) {
	dir := t.TempDir()
	entry := testLockEntry()
	entry.Artifacts[0].Location.Value = "HTTPS://Example.test/a%2Fb?token=x%2Fy"
	entry.Evidence = []lockEvidence{
		{Kind: resolution.EvidenceKindReleaseMetadata, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/z-metadata"}, Hash: "sha256:" + strings.Repeat("a", 64)},
		{Kind: resolution.EvidenceKindReleaseIndex, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/a-index"}, Hash: "sha256:" + strings.Repeat("b", 64)},
		{Kind: resolution.EvidenceKindReleaseIndex, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "./b-index"}, Hash: "sha256:" + strings.Repeat("c", 64)},
	}
	originalEvidence := append([]lockEvidence(nil), entry.Evidence...)
	lock := LockFile{Version: lockFileVersion, Revision: 0, Drivers: []lockInfo{entry, {
		Name: "local", Version: semver.MustParse("0.1.0"),
		Source: lockSource{Type: "path", Path: "./packages/local.tar.gz"},
		Artifacts: []lockArtifact{{
			Target: resolution.Target{OS: "windows", Arch: "amd64"}, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "./packages/local.tar.gz"}, Hash: "sha256:" + strings.Repeat("c", 64), Size: int64Pointer(0), Format: "tar.gz",
		}, {
			Target: resolution.Target{OS: "macos", Arch: "arm64"}, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "/var/cache/local.tar.gz"}, Hash: "sha256:" + strings.Repeat("d", 64), Size: int64Pointer(1), Format: "tar.gz",
		}},
	}}}
	firstPath := filepath.Join(dir, "first.lock")
	require.NoError(t, writeLockFileAtomic(firstPath, lock))
	first, err := os.ReadFile(firstPath)
	require.NoError(t, err)
	assert.Equal(t, originalEvidence, lock.Drivers[0].Evidence, "canonical serialization must not reorder caller evidence")

	lock.Drivers[0].Artifacts[0], lock.Drivers[0].Artifacts[1] = lock.Drivers[0].Artifacts[1], lock.Drivers[0].Artifacts[0]
	for i, j := 0, len(lock.Drivers[0].Evidence)-1; i < j; i, j = i+1, j-1 {
		lock.Drivers[0].Evidence[i], lock.Drivers[0].Evidence[j] = lock.Drivers[0].Evidence[j], lock.Drivers[0].Evidence[i]
	}
	reversedEvidence := append([]lockEvidence(nil), lock.Drivers[0].Evidence...)
	secondPath := filepath.Join(dir, "second.lock")
	require.NoError(t, writeLockFileAtomic(secondPath, lock))
	second, err := os.ReadFile(secondPath)
	require.NoError(t, err)
	assert.Equal(t, reversedEvidence, lock.Drivers[0].Evidence, "canonical serialization must preserve caller evidence order")
	assert.Equal(t, string(first), string(second), "serialization should be canonical regardless of input order")

	loaded, err := loadLockFile(firstPath)
	require.NoError(t, err)
	assert.Equal(t, lockFileVersion, loaded.Version)
	assert.Len(t, loaded.Drivers, 2)
	linux := loaded.lockinfo["example"].Artifacts[0]
	assert.Equal(t, "gnu", linux.Target.LibC)
	assert.Equal(t, "v1", linux.Target.Variant)
	assert.Equal(t, "HTTPS://Example.test/a%2Fb?token=x%2Fy", linux.Location.Value,
		"URL case, percent encoding, query, and spelling are preserved")
	assert.Equal(t, []string{"libc.so.6", "libssl.so.3"}, linux.HostRequirements.Libs)
	assert.Equal(t, "2.17", linux.HostRequirements.GLibCMin)
	assert.Equal(t, []lockNamedRequirement{{Name: "git", Min: "2.40"}}, linux.HostRequirements.Bins)
	assert.Equal(t, []lockEvidence{
		{Kind: resolution.EvidenceKindReleaseIndex, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "./b-index"}, Hash: "sha256:" + strings.Repeat("c", 64)},
		{Kind: resolution.EvidenceKindReleaseIndex, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/a-index"}, Hash: "sha256:" + strings.Repeat("b", 64)},
		{Kind: resolution.EvidenceKindReleaseMetadata, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/z-metadata"}, Hash: "sha256:" + strings.Repeat("a", 64)},
	}, loaded.lockinfo["example"].Evidence)
	locations := []resolution.ArtifactLocation{
		loaded.lockinfo["local"].Artifacts[0].Location,
		loaded.lockinfo["local"].Artifacts[1].Location,
	}
	assert.Contains(t, locations, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "./packages/local.tar.gz"})
	assert.Contains(t, locations, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "/var/cache/local.tar.gz"})
	assert.NotContains(t, string(first), "platform =")
	assert.Contains(t, string(first), "[drivers.artifacts.target]")
	assert.Contains(t, string(first), "package_version = 2")
	var zeroSizePreserved bool
	for _, artifact := range loaded.lockinfo["local"].Artifacts {
		if artifact.Target == (resolution.Target{OS: "windows", Arch: "amd64"}) {
			zeroSizePreserved = artifact.Size != nil && *artifact.Size == 0
		}
	}
	assert.True(t, zeroSizePreserved, "an explicit zero size is distinct from an omitted size")
	localRelease := loaded.lockinfo["local"].resolvedRelease()
	localRoundTrip, err := lockInfoFromResolvedRelease("local", localRelease)
	require.NoError(t, err)
	locations = []resolution.ArtifactLocation{localRoundTrip.Artifacts[0].Location, localRoundTrip.Artifacts[1].Location}
	assert.Contains(t, locations, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "./packages/local.tar.gz"})
	assert.Contains(t, locations, resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "/var/cache/local.tar.gz"})
}

func TestLockArtifactRejectsInvalidLocations(t *testing.T) {
	tests := []struct {
		name     string
		location resolution.ArtifactLocation
	}{
		{name: "empty kind", location: resolution.ArtifactLocation{Value: "https://example.test/archive"}},
		{name: "unknown kind", location: resolution.ArtifactLocation{Kind: "other", Value: "value"}},
		{name: "empty URL", location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL}},
		{name: "relative URL", location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "archive.tar.gz"}},
		{name: "file URL", location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "file:///tmp/archive.tar.gz"}},
		{name: "opaque URL", location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https:archive.tar.gz"}},
		{name: "userinfo URL", location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://user@example.test/archive"}},
		{name: "fragment URL", location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/archive#part"}},
		{name: "empty fragment URL", location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/archive#"}},
		{name: "empty path", location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath}},
		{name: "NUL path", location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "archive\x00.tar.gz"}},
		{name: "URI path", location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "https://example.test/archive"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artifact := testLockArtifact("linux_amd64", "a", 10)
			artifact.Location = test.location
			assert.Error(t, validateLockArtifacts([]lockArtifact{artifact}))
		})
	}
}

func TestLockFileV2AllowsPartialArtifactSetAndReplayNeedsNoDiscovery(t *testing.T) {
	entry := testLockEntry()
	entry.Artifacts = entry.Artifacts[:1]
	artifact, err := selectLockedArtifact(entry, "linux_amd64_v1", false)
	require.NoError(t, err)
	assert.Equal(t, entry.Artifacts[0].Hash, artifact.Hash)
	assert.Equal(t, entry.Artifacts[0].HostRequirements, artifact.HostRequirements)
	_, err = selectLockedArtifact(entry, "linux_amd64", false)
	assert.ErrorIs(t, err, ErrLockRefreshRequired, "lock replay requires an exact target including variant")

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

func TestPackslipLockRequiresPackageVersionTwoForEveryArtifact(t *testing.T) {
	for artifactIndex := range testLockEntry().Artifacts {
		t.Run(strconv.Itoa(artifactIndex), func(t *testing.T) {
			entry := testLockEntry()
			entry.Artifacts[artifactIndex].PackageVersion = 0

			err := validateLockInfo(entry)
			require.ErrorContains(t, err, "packslip artifact")
			require.ErrorContains(t, err, "package_version = 2")
		})
	}
}

func TestPackslipLockWithoutPackageVersionIsRejectedDuringLoad(t *testing.T) {
	tests := []struct {
		name        string
		replacement string
	}{
		{name: "missing", replacement: ""},
		{name: "zero", replacement: "package_version = 0\n"},
		{name: "unknown", replacement: "package_version = 3\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dbc.lock")
			require.NoError(t, writeLockFileAtomic(path, LockFile{Version: lockFileVersion, Drivers: []lockInfo{testLockEntry()}}))
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			data = []byte(strings.ReplaceAll(string(data), "package_version = 2\n", test.replacement))
			require.NoError(t, os.WriteFile(path, data, 0o600))

			_, err = loadLockFile(path)
			if test.name != "unknown" {
				require.ErrorContains(t, err, "packslip artifact")
				require.ErrorContains(t, err, "package_version = 2")
			} else {
				require.ErrorContains(t, err, "unsupported dbc package version 3")
			}
		})
	}
}

func TestPackageVersionZeroRemainsAllowedForRegistryAndPathLocks(t *testing.T) {
	sources := []lockSource{
		{Type: "registry", URL: "https://registry.example.test"},
		{Type: "path", Path: "./drivers/example"},
	}
	for _, source := range sources {
		t.Run(source.Type, func(t *testing.T) {
			entry := testLockEntry()
			entry.Source = source
			for i := range entry.Artifacts {
				entry.Artifacts[i].PackageVersion = 0
			}

			require.NoError(t, validateLockInfo(entry))
			path := filepath.Join(t.TempDir(), "dbc.lock")
			require.NoError(t, writeLockFileAtomic(path, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))
			loaded, err := loadLockFile(path)
			require.NoError(t, err)
			for _, artifact := range loaded.lockinfo[entry.Name].Artifacts {
				assert.Zero(t, artifact.PackageVersion)
			}
		})
	}
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
	assert.Contains(t, string(data), "[[drivers.evidence]]")
	assert.Contains(t, string(data), "[drivers.evidence.location]")
	assert.Contains(t, string(data), "kind = 'release-metadata'")
	assert.NotContains(t, string(data), "bundle_url =")

	entry := testLockEntry()
	entry.Evidence[0].Hash = ""
	err = validateLockInfo(entry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no hash")
}

func TestLockFileV2RejectsLegacyEvidenceWireForms(t *testing.T) {
	base := "version = 2\nrevision = 0\n\n[[drivers]]\nname = 'example'\nversion = '1.2.3'\n\n[drivers.source]\ntype = 'registry'\nurl = 'https://registry.example.test'\n"
	artifact := "\n[[drivers.artifacts]]\nformat = 'tar.gz'\nhash = 'sha256:" + strings.Repeat("a", 64) + "'\nsize = 1\n[drivers.artifacts.target]\nos = 'linux'\narch = 'amd64'\nlibc = 'gnu'\n[drivers.artifacts.location]\nkind = 'url'\nvalue = 'https://assets.example.test/archive.tar.gz'\n"
	tests := []struct {
		name     string
		evidence string
		wantErr  string
	}{
		{
			name:     "old single table",
			evidence: "\n[drivers.evidence]\nbundle_url = 'https://example.test/bundle'\nbundle_hash = 'sha256:" + strings.Repeat("e", 64) + "'\n",
			wantErr:  "obsolete source-specific fields",
		},
		{
			name:     "old fields in new evidence array",
			evidence: "\n[[drivers.evidence]]\nbundle_url = 'https://example.test/bundle'\nbundle_hash = 'sha256:" + strings.Repeat("e", 64) + "'\n",
			wantErr:  "obsolete source-specific fields",
		},
		{
			name:     "old field alongside valid new evidence",
			evidence: "\n[[drivers.evidence]]\nkind = 'release-metadata'\nhash = 'sha256:" + strings.Repeat("e", 64) + "'\nbundle_url = 'https://example.test/bundle'\n[drivers.evidence.location]\nkind = 'url'\nvalue = 'https://example.test/bundle'\n",
			wantErr:  "obsolete source-specific fields",
		},
		{
			name:     "empty old field alongside valid new evidence",
			evidence: "\n[[drivers.evidence]]\nkind = 'release-metadata'\nhash = 'sha256:" + strings.Repeat("e", 64) + "'\nbundle_url = ''\n[drivers.evidence.location]\nkind = 'url'\nvalue = 'https://example.test/bundle'\n",
			wantErr:  "obsolete source-specific fields",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dbc.lock")
			require.NoError(t, os.WriteFile(path, []byte(base+test.evidence+artifact), 0o600))
			_, err := loadLockFile(path)
			assert.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestLockFileV2RejectsObsoleteArtifactSelectorFields(t *testing.T) {
	fields := []struct{ name, value string }{
		{name: "platform", value: `platform = "linux_amd64"`},
		{name: "os", value: `os = "linux"`},
		{name: "arch", value: `arch = "amd64"`},
		{name: "libc", value: `libc = "gnu"`},
		{name: "variant", value: `variant = "v1"`},
	}
	for _, field := range fields {
		for _, withTarget := range []bool{false, true} {
			targetCase := "alone"
			if withTarget {
				targetCase = "with_target"
			}
			t.Run(field.name+"/"+targetCase, func(t *testing.T) {
				contents := "version = 2\nrevision = 0\n\n[[drivers]]\nname = 'example'\nversion = '1.2.3'\n\n[drivers.source]\ntype = 'registry'\nurl = 'https://registry.example.test'\n\n[[drivers.artifacts]]\nformat = 'tar.gz'\nhash = 'sha256:" + strings.Repeat("a", 64) + "'\nsize = 1\n" + field.value + "\n\n[drivers.artifacts.location]\nkind = 'url'\nvalue = 'https://assets.example.test/archive.tar.gz'\n"
				if withTarget {
					contents += "[drivers.artifacts.target]\nos = 'linux'\narch = 'amd64'\nlibc = 'gnu'\n"
				}
				path := filepath.Join(t.TempDir(), "dbc.lock")
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				_, err := loadLockFile(path)
				assert.ErrorContains(t, err, "obsolete selector fields")
			})
		}
	}
}

func TestLockFileV2RejectsObsoleteDirectArtifactLocationFields(t *testing.T) {
	for _, field := range []string{"url", "path"} {
		for _, withLocation := range []bool{false, true} {
			t.Run(field+"/with_location="+strconv.FormatBool(withLocation), func(t *testing.T) {
				contents := "version = 2\nrevision = 0\n\n[[drivers]]\nname = 'example'\nversion = '1.2.3'\n\n[drivers.source]\ntype = 'registry'\nurl = 'https://registry.example.test'\n\n[[drivers.artifacts]]\n"
				contents += field + " = ''\nformat = 'tar.gz'\nhash = 'sha256:" + strings.Repeat("a", 64) + "'\nsize = 1\n[drivers.artifacts.target]\nos = 'linux'\narch = 'amd64'\nlibc = 'gnu'\n"
				if withLocation {
					contents += "\n[drivers.artifacts.location]\nkind = 'url'\nvalue = 'https://assets.example.test/archive.tar.gz'\n"
				}
				path := filepath.Join(t.TempDir(), "dbc.lock")
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				_, err := loadLockFile(path)
				assert.ErrorContains(t, err, "obsolete direct URL or path")
			})
		}
	}
}

func TestLockFileV1MigrationPreservesLibraryProofAndRequiresSamePlatformVerification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbc.lock")
	legacyHash := strings.Repeat("d", 64)
	body := "version = 1\n\n[[drivers]]\nname = \"example\"\nversion = \"1.2.3\"\nplatform = \"darwin_aarch64\"\nchecksum = \"" + legacyHash + "\"\n"
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
	assert.Equal(t, "darwin_aarch64", migrated.Legacy.Platform, "the original v1 tuple remains intact")
	assert.NotEqual(t, "sha256:"+legacyHash, migrated.Artifacts[0].Hash, "v1 library checksum must not become an archive hash")
	assert.NoError(t, verifyLegacyLibraryProof(migrated, "macos_arm64", legacyHash))
	assert.Error(t, verifyLegacyLibraryProof(migrated, "macos_arm64", strings.Repeat("0", 64)))
	assert.Equal(t, legacyHash, migrated.legacyChecksumFor("macos_arm64"), "legacy tuple aliases compare canonically")
	v2Path := filepath.Join(t.TempDir(), "migrated.lock")
	require.NoError(t, writeLockFileAtomic(v2Path, LockFile{Version: 2, Drivers: []lockInfo{migrated}}))
	reloaded, err := loadLockFile(v2Path)
	require.NoError(t, err)
	require.NotNil(t, reloaded.lockinfo["example"].Legacy)
	assert.Equal(t, legacyHash, reloaded.lockinfo["example"].Legacy.LibraryHash)

	otherPlatform, err := migrateV1Entry(old.lockinfo["example"], release, "linux_amd64", nil)
	require.NoError(t, err, "migration on another platform may retain but not apply the old proof")
	require.NotNil(t, otherPlatform.Legacy)
	assert.Equal(t, "darwin_aarch64", otherPlatform.Legacy.Platform)
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
	newArtifact := testLockArtifact("windows_amd64", "c", 22)
	newArtifact.PackageVersion = 2
	contradictory := existing.Artifacts[0]
	contradictory.Hash = "sha256:" + strings.Repeat("f", 64)
	contradictory.Size = int64Pointer(999)
	refreshed.Artifacts = []lockArtifact{contradictory, newArtifact}
	_, err := refreshLockEntry(existing, refreshed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contradicts")

	refreshed.Artifacts = []lockArtifact{existing.Artifacts[0], newArtifact}
	merged, err := refreshLockEntry(existing, refreshed)
	require.NoError(t, err)
	assert.Len(t, merged.Artifacts, 3)
}

func TestRefreshRejectsUnprovenPackslipLockInsteadOfBackfilling(t *testing.T) {
	existing := testLockEntry()
	existing.Artifacts[0].PackageVersion = 0
	refreshed := testLockEntry()

	_, err := refreshLockEntry(existing, refreshed)
	require.ErrorContains(t, err, "existing lock entry")
	require.ErrorContains(t, err, "packslip artifact")
	require.ErrorContains(t, err, "package_version = 2")
}

func TestRefreshBackfillsMissingNonPackslipPackageVersion(t *testing.T) {
	sources := []lockSource{
		{Type: "registry", URL: "https://registry.example.test"},
		{Type: "path", Path: "./drivers/example"},
	}
	for _, source := range sources {
		t.Run(source.Type, func(t *testing.T) {
			existing := testLockEntry()
			existing.Source = source
			for i := range existing.Artifacts {
				existing.Artifacts[i].PackageVersion = 0
			}
			refreshed := cloneLockInfo(existing)
			for i := range refreshed.Artifacts {
				refreshed.Artifacts[i].PackageVersion = 2
			}

			merged, err := refreshLockEntry(existing, refreshed)
			require.NoError(t, err)
			for _, artifact := range merged.Artifacts {
				require.Equal(t, 2, artifact.PackageVersion)
			}
		})
	}
}

func TestRefreshReplacesEvidenceWithLatestCanonicalSnapshot(t *testing.T) {
	oldIndex := lockEvidence{
		Kind:     resolution.EvidenceKindReleaseIndex,
		Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/index"},
		Hash:     "sha256:" + strings.Repeat("a", 64),
	}
	oldMetadata := lockEvidence{
		Kind:     resolution.EvidenceKindReleaseMetadata,
		Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/old"},
		Hash:     "sha256:" + strings.Repeat("b", 64),
	}
	existing := testLockEntry()
	existing.Evidence = []lockEvidence{oldMetadata, oldIndex}

	newIndex := oldIndex
	newIndex.Hash = "sha256:" + strings.Repeat("c", 64)
	newMetadata := lockEvidence{
		Kind:     resolution.EvidenceKindReleaseMetadata,
		Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/new"},
		Hash:     "sha256:" + strings.Repeat("d", 64),
	}
	refreshed := testLockEntry()
	refreshed.Evidence = []lockEvidence{newMetadata, newIndex}
	want := canonicalLockEvidence(refreshed.Evidence)

	merged, err := refreshLockEntry(existing, refreshed)
	require.NoError(t, err, "an index's observed hash may change across refresh snapshots")
	assert.Equal(t, want, merged.Evidence)
	assert.NotContains(t, merged.Evidence, oldMetadata, "refresh replaces stale evidence entries")

	refreshed.Evidence[0].Hash = "sha256:" + strings.Repeat("f", 64)
	assert.Equal(t, want, merged.Evidence, "the merged snapshot must not alias the refreshed evidence slice")

	refreshed.Evidence = []lockEvidence{newMetadata}
	latestOnly, err := refreshLockEntry(existing, refreshed)
	require.NoError(t, err)
	assert.Equal(t, []lockEvidence{newMetadata}, latestOnly.Evidence,
		"refresh replaces the full evidence set rather than retaining omitted records")
}

func TestRefreshCannotReplaceSameTargetArtifactMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*lockArtifact)
	}{
		{name: "location", mutate: func(a *lockArtifact) { a.Location.Value = "https://other.example.test/archive.tar.gz" }},
		{name: "format", mutate: func(a *lockArtifact) { a.Format = "tgz" }},
		{name: "requirements", mutate: func(a *lockArtifact) { a.HostRequirements.OSMin = "99" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := testLockEntry()
			refreshed := testLockEntry()
			test.mutate(&refreshed.Artifacts[0])
			_, err := refreshLockEntry(existing, refreshed)
			assert.ErrorContains(t, err, "contradicts locked artifact")
		})
	}
}

func TestRefreshAcceptsEquivalentHostRequirementsAfterLockRoundTrip(t *testing.T) {
	dir := t.TempDir()
	original := testLockEntry()
	original.Artifacts[0].HostRequirements.Bins = append(original.Artifacts[0].HostRequirements.Bins,
		lockNamedRequirement{Name: "java", Min: "17"})
	path := filepath.Join(dir, "dbc.lock")
	require.NoError(t, writeLockFileAtomic(path, LockFile{Version: lockFileVersion, Drivers: []lockInfo{original}}))
	loaded, err := loadLockFile(path)
	require.NoError(t, err)
	_, err = refreshLockEntry(loaded.lockinfo["example"], original)
	require.NoError(t, err, "persisted sorted requirements must match the same unsorted source metadata")

	withoutRequirements := testLockEntry()
	withoutRequirements.Artifacts[0].HostRequirements = lockHostRequirements{}
	require.NoError(t, writeLockFileAtomic(path, LockFile{Version: lockFileVersion, Drivers: []lockInfo{withoutRequirements}}))
	loaded, err = loadLockFile(path)
	require.NoError(t, err)
	withoutRequirements.Artifacts[0].HostRequirements.Libs = []string{}
	withoutRequirements.Artifacts[0].HostRequirements.Bins = []lockNamedRequirement{}
	_, err = refreshLockEntry(loaded.lockinfo["example"], withoutRequirements)
	require.NoError(t, err, "empty requirement slices are equivalent to omitted slices")
}

func TestLockArtifactsAllowSharedLocationOnlyWithConsistentMetadata(t *testing.T) {
	first := testLockArtifact("linux_amd64", "a", 10)
	second := testLockArtifact("macos_arm64", "a", 10)
	second.Location = first.Location
	assert.NoError(t, validateLockArtifacts([]lockArtifact{first, second}))
	second.Size = int64Pointer(11)
	assert.ErrorContains(t, validateLockArtifacts([]lockArtifact{first, second}), "conflicting hash or size")
	second.Size = int64Pointer(10)
	second.Hash = "sha256:" + strings.Repeat("b", 64)
	assert.ErrorContains(t, validateLockArtifacts([]lockArtifact{first, second}), "conflicting hash or size")
	second = testLockArtifact("macos_arm64", "b", 11)
	second.Location = resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: first.Location.Value}
	assert.NotEqual(t, first.Location, second.Location, "same value with distinct kinds has a distinct identity")
}

func TestLockRoundTripPreservesOneArtifactLocationSharedBySeveralTargets(t *testing.T) {
	release := testResolvedRelease()
	shared := release.Artifacts[0]
	shared.Target = resolution.Target{OS: "macos", Arch: "arm64"}
	release.Artifacts = append(release.Artifacts, shared)
	require.NoError(t, resolution.ValidateResolvedRelease(release))

	entry, err := lockInfoFromResolvedRelease("example", release)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "dbc.lock")
	require.NoError(t, writeLockFileAtomic(path, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))
	loaded, err := loadLockFile(path)
	require.NoError(t, err)
	assert.Equal(t, release, loaded.lockinfo["example"].resolvedRelease())
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
	assert.Contains(t, err.Error(), "hash and size must either both be present or both be absent")

	release = testResolvedRelease()
	release.Artifacts = append(release.Artifacts, release.Artifacts[0])
	_, err = lockInfoFromResolvedRelease("example", release)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate target")
}

func TestSyncAdapterReusesCompleteV2SnapshotWithoutRegistryHashes(t *testing.T) {
	platform := config.PlatformTuple()
	registryURL, err := url.Parse("https://registry.example.test")
	require.NoError(t, err)
	entry := lockInfo{
		Name:    "example",
		Version: semver.MustParse("1.2.3"),
		Source:  lockSource{Type: "registry", URL: registryURL.String()},
		Artifacts: []lockArtifact{{
			Target:   testTarget(platform),
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://assets.example.test/archive.tar.gz"}, Hash: "sha256:" + strings.Repeat("a", 64), Size: int64Pointer(10), Format: "tar.gz",
		}},
	}
	item, err := installItemFromLockedArtifact("example", entry, entry.Artifacts[0])
	require.NoError(t, err)
	assert.Equal(t, registryURL.String(), item.Driver.Registry.BaseURL.String())
	assert.Equal(t, "https://assets.example.test/archive.tar.gz", item.Package.Path.String(), "artifact host is independent of source identity")
	assert.Equal(t, entry.Artifacts[0].Hash, item.Package.ArtifactHash)
	assert.Equal(t, *entry.Artifacts[0].Size, *item.Package.ArtifactSize)
	assert.True(t, canReuseLockedEntry(item))
	item.ArtifactFormat = "tgz"
	assert.False(t, canReuseLockedEntry(item), "a locked artifact with a different format is not the selected artifact proof")
	item.ArtifactFormat = entry.Artifacts[0].Format
	item.HostRequirements.Libs = []string{"libc.so.6"}
	assert.False(t, canReuseLockedEntry(item), "a locked artifact with different host requirements is not the selected artifact proof")
	item.HostRequirements.Libs = nil
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

func TestSyncAdapterPreservesLockedURLSpellingWhenReusingSnapshot(t *testing.T) {
	platform := config.PlatformTuple()
	const rawURL = "HTTPS://Example.test/a%2Fb?token=x%2Fy"
	registryURL, err := url.Parse("https://registry.example.test")
	require.NoError(t, err)
	entry := lockInfo{
		Name:    "example",
		Version: semver.MustParse("1.2.3"),
		Source:  lockSource{Type: "registry", URL: registryURL.String()},
		Artifacts: []lockArtifact{{
			Target: testTarget(platform),
			Location: resolution.ArtifactLocation{
				Kind: resolution.ArtifactLocationURL, Value: rawURL,
			},
			Hash: "sha256:" + strings.Repeat("a", 64), Size: int64Pointer(10), Format: "tar.gz",
		}},
	}
	item, err := installItemFromLockedArtifact("example", entry, entry.Artifacts[0])
	require.NoError(t, err)

	updated, err := lockEntryForItem(item)
	require.NoError(t, err)
	require.Len(t, updated.Artifacts, 1)
	assert.Equal(t, rawURL, updated.Artifacts[0].Location.Value,
		"reusing a locked artifact must preserve the original URL spelling")
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
	assert.Contains(t, err.Error(), "hash and size must either both be present or both be absent")
}

func TestSyncAdapterRejectsPathArtifactForRegistryReplay(t *testing.T) {
	entry := testRegistryLockEntryForPlatform(config.PlatformTuple())
	entry.Artifacts[0].Location = resolution.ArtifactLocation{Kind: resolution.ArtifactLocationPath, Value: "./archive.tar.gz"}
	_, err := installItemFromLockedArtifact("example", entry, entry.Artifacts[0])
	assert.ErrorContains(t, err, "not a remote URL artifact")
}

func TestSyncAdapterVerifiesLegacyLibraryProofBeforeReusingV2Entry(t *testing.T) {
	platform := config.PlatformTuple()
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
			Target:   testTarget(platform),
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://registry.example.test/archive.tar.gz"}, Hash: "sha256:" + strings.Repeat("a", 64), Size: int64Pointer(10),
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
	assert.Equal(t, entry.Artifacts[0].Location.Value, items[0].Package.Path.String())
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
	return lockInfo{
		Name:    "example",
		Version: semver.MustParse("1.2.3"),
		Source:  lockSource{Type: "registry", URL: "https://registry.example.test"},
		Artifacts: []lockArtifact{{
			Target: testTarget(platform),
			Format: "tar.gz", Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://assets.example.test/" + platform + ".tar.gz"},
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

func TestV2RegistryReplayAllowsExplicitPrereleaseConstraintOffline(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "dbc.toml")
	entry := testRegistryLockEntryForPlatform(config.PlatformTuple())
	entry.Version = semver.MustParse("1.2.3-beta.1")
	lockPath := filepath.Join(filepath.Dir(projectPath), "dbc.lock")
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: 2, Drivers: []lockInfo{entry}}))
	require.NoError(t, os.WriteFile(projectPath, []byte("[drivers]\n[drivers.example]\n"), 0o600))
	constraint, err := semver.NewConstraint("=1.2.3-beta.1")
	require.NoError(t, err)

	discoveryCalls := 0
	model := syncModel{
		baseModel: baseModel{getDriverRegistry: func() ([]dbc.Driver, error) {
			discoveryCalls++
			return nil, errors.New("registry unavailable")
		}},
		Path: projectPath,
	}
	_, cmd := model.Update(driversListMsg{path: projectPath, list: DriversList{
		Drivers: map[string]driverSpec{"example": {Version: constraint}},
	}})
	require.NotNil(t, cmd)
	msg := cmd()
	items, ok := msg.([]installItem)
	require.True(t, ok, "expected explicit prerelease constraint to replay lock, got %T", msg)
	require.Len(t, items, 1)
	assert.Equal(t, "1.2.3-beta.1", items[0].Package.Version.String())
	assert.Zero(t, discoveryCalls, "an explicit matching prerelease constraint must not trigger registry discovery")
}

func TestLockReplayRejectsMuslForGenericLinuxTargetAndAmbiguousArtifacts(t *testing.T) {
	entry := testLockEntry()
	entry.Artifacts = []lockArtifact{{
		Target: resolution.Target{OS: "linux", Arch: "amd64", LibC: "musl"}, Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/musl.tar.gz"},
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
	release.Evidence[0].Hash = "sha256:" + strings.Repeat("f", 64)
	assert.NotEqual(t, release.Evidence[0].Hash, entry.Evidence[0].Hash,
		"lock conversion must copy the evidence slice")
	entry.Evidence[0].Hash = "sha256:" + strings.Repeat("0", 64)
	assert.NotEqual(t, entry.Evidence[0].Hash, got.Evidence[0].Hash,
		"resolved release conversion must copy the evidence slice")
	release.Artifacts[0].HostRequirements.Libs[0] = "changed.so"
	release.Artifacts[0].HostRequirements.Bins[0].Name = "changed"
	assert.Equal(t, []string{"libc.so.6"}, entry.Artifacts[0].HostRequirements.Libs,
		"lock conversion must copy host library requirements")
	assert.Equal(t, []lockNamedRequirement{{Name: "git", Min: "2.40"}}, entry.Artifacts[0].HostRequirements.Bins,
		"lock conversion must copy host binary requirements")
	got.Artifacts[0].HostRequirements.Libs[0] = "changed-again.so"
	got.Artifacts[0].HostRequirements.Bins[0].Name = "changed-again"
	assert.Equal(t, []string{"libc.so.6"}, entry.resolvedRelease().Artifacts[0].HostRequirements.Libs,
		"resolved release conversion must copy host library requirements")
	assert.Equal(t, []resolution.NamedRequirement{{Name: "git", Min: "2.40"}}, entry.resolvedRelease().Artifacts[0].HostRequirements.Bins,
		"resolved release conversion must copy host binary requirements")
}

func TestResolvedTGZArtifactReplaysThroughPackageValidation(t *testing.T) {
	archivePath := filepath.Join("testdata", "test-driver-1.tar.gz")
	archive, err := os.Open(archivePath)
	require.NoError(t, err)
	defer archive.Close()
	info, err := archive.Stat()
	require.NoError(t, err)
	size := info.Size()
	hash, err := checksumFile(archive, archive.Name())
	require.NoError(t, err)
	require.NoError(t, archive.Close())

	packageURL, err := url.Parse("https://assets.example.test/test-driver-1.tgz")
	require.NoError(t, err)
	release := resolution.ResolvedRelease{
		DriverID: "test-driver-1",
		Version:  "1.0.0",
		Source:   resolution.SourceSpec{Type: "registry", Reference: testRegistry.BaseURL.String()},
		Artifacts: []resolution.Artifact{{
			Target:   testTarget(config.PlatformTuple()),
			Format:   "tgz",
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: packageURL.String()},
			Hash:     "sha256:" + hash,
			Size:     &size,
		}},
	}
	entry, err := lockInfoFromResolvedRelease("test-driver-1", release)
	require.NoError(t, err)
	lockPath := filepath.Join(t.TempDir(), "dbc.lock")
	require.NoError(t, writeLockFileAtomic(lockPath, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))
	loaded, err := loadLockFile(lockPath)
	require.NoError(t, err)
	selected, err := selectLockedArtifact(loaded.lockinfo["test-driver-1"], config.PlatformTuple(), false)
	require.NoError(t, err)
	assert.Equal(t, "tgz", selected.Format)
	item, err := installItemFromLockedArtifact("test-driver-1", loaded.lockinfo["test-driver-1"], selected)
	require.NoError(t, err)
	assert.Equal(t, "tgz", item.ArtifactFormat)

	model := syncModel{baseModel: baseModel{downloadPkg: func(dbc.PkgInfo) (*os.File, error) {
		return os.Open(archivePath)
	}}}
	prepared, err := model.prepareInstallItems(context.Background(), []installItem{item})
	require.NoError(t, err)
	defer closePreparedArchives(prepared.items)
	require.Len(t, prepared.items, 1)
	require.NotNil(t, prepared.items[0].Validation)
	assert.Equal(t, "tgz", prepared.lock.Drivers[0].Artifacts[0].Format,
		"candidate lock must preserve the source-declared format")
	assert.Equal(t, entry.Artifacts[0], prepared.lock.Drivers[0].Artifacts[0])
}

func TestSelectedArtifactMetadataCopiesHostRequirements(t *testing.T) {
	requirements := resolution.HostRequirements{
		OSMin:    "3.2",
		GLibCMin: "2.17",
		Libs:     []string{"libssl.so.3"},
		Bins:     []resolution.NamedRequirement{{Name: "git", Min: "2.40"}},
	}
	item := installItem{}
	require.NoError(t, setSelectedArtifactMetadata(&item, resolution.Artifact{Format: "tgz", HostRequirements: requirements}))
	requirements.Libs[0] = "changed"
	requirements.Bins[0].Name = "changed"
	assert.Equal(t, "tgz", item.ArtifactFormat)
	assert.Equal(t, []string{"libssl.so.3"}, item.HostRequirements.Libs)
	assert.Equal(t, []resolution.NamedRequirement{{Name: "git", Min: "2.40"}}, item.HostRequirements.Bins)
}

func TestResolvedReleaseHostRequirementsSurviveLockWriteAndReload(t *testing.T) {
	release := testResolvedRelease()
	release.Artifacts[0].Format = "tgz"
	entry, err := lockInfoFromResolvedRelease("example", release)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "dbc.lock")
	require.NoError(t, writeLockFileAtomic(path, LockFile{Version: lockFileVersion, Drivers: []lockInfo{entry}}))
	loaded, err := loadLockFile(path)
	require.NoError(t, err)
	artifact, err := selectLockedArtifact(loaded.lockinfo["example"], "linux_amd64_gnu_v1", false)
	require.NoError(t, err)
	assert.Equal(t, "tgz", artifact.Format)
	assert.Equal(t, lockArtifactFromResolved(release.Artifacts[0]).HostRequirements, artifact.HostRequirements)
	assert.Equal(t, release.Artifacts[0].HostRequirements, loaded.lockinfo["example"].resolvedRelease().Artifacts[0].HostRequirements)
}

func TestInstallableArtifactFormatValidation(t *testing.T) {
	for _, format := range []string{"", "tar.gz", "tgz"} {
		t.Run("accept "+format, func(t *testing.T) {
			assert.NoError(t, validateInstallableArtifactFormat(format))
		})
	}
	for _, format := range []string{"zip", "tar.xz", "tar"} {
		t.Run("reject "+format, func(t *testing.T) {
			err := validateInstallableArtifactFormat(format)
			require.Error(t, err)
			assert.Contains(t, err.Error(), format)
			assert.Contains(t, err.Error(), "supported formats: tar.gz, tgz")
		})
	}
}

func TestUnsupportedHostRequirementsFailClosedBeforePreparationOrEnsure(t *testing.T) {
	tests := []struct {
		name         string
		requirements resolution.HostRequirements
		want         string
	}{
		{name: "os minimum", requirements: resolution.HostRequirements{OSMin: "3.2.0"}, want: `os_min="3.2.0"`},
		{name: "glibc minimum", requirements: resolution.HostRequirements{GLibCMin: "2.17"}, want: `glibc_min="2.17"`},
		{name: "libraries", requirements: resolution.HostRequirements{Libs: []string{"libz.so", "liba.so"}}, want: `libs=["liba.so", "libz.so"]`},
		{name: "binaries", requirements: resolution.HostRequirements{Bins: []resolution.NamedRequirement{{Name: "zstd", Min: "1.5"}, {Name: "git"}}}, want: `bins=["git", "zstd" (min "1.5")]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("ADBC_DRIVER_PATH", root)
			libraryPath := filepath.Join(root, "sentinel.so")
			const sentinel = "keep-existing-runtime-bytes"
			require.NoError(t, os.WriteFile(libraryPath, []byte(sentinel), 0o600))
			installed := config.DriverInfo{ID: "example", Name: "Existing Example", Version: semver.MustParse("1.2.3"), Source: "dbc"}
			installed.Driver.Shared.Set(config.PlatformTuple(), libraryPath)
			cfg := config.Config{Level: config.ConfigEnv, Location: root}
			require.NoError(t, config.CreateManifest(cfg, installed))
			loaded, err := config.GetDriver(cfg, "example")
			require.NoError(t, err)

			registryURL, err := url.Parse("https://registry.example.test")
			require.NoError(t, err)
			packageURL, err := url.Parse("https://assets.example.test/example.tgz")
			require.NoError(t, err)
			item := installItem{
				Driver:           dbc.Driver{Path: "example", Registry: &dbc.Registry{BaseURL: registryURL}},
				Package:          dbc.PkgInfo{Version: semver.MustParse("1.2.3"), PlatformTuple: config.PlatformTuple(), Path: packageURL},
				ArtifactFormat:   "tgz",
				HostRequirements: tt.requirements,
			}
			downloadCalls, ensureCalls := 0, 0
			worker := newSyncWorker()
			worker.hooks.ensurePackage = func(context.Context, config.Config, string, config.ExpectedPackageMetadata, config.InstallOptions, config.EnsurePackageCallbacks) (config.EnsurePackageResult, error) {
				ensureCalls++
				return config.EnsurePackageResult{}, nil
			}
			model := syncModel{
				baseModel: baseModel{downloadPkg: func(dbc.PkgInfo) (*os.File, error) {
					downloadCalls++
					return nil, errors.New("unexpected download")
				}},
				LockFilePath: filepath.Join(root, "dbc.lock"),
				cfg:          config.Config{Level: config.ConfigEnv, Location: root, Exists: true, Drivers: map[string]config.DriverInfo{"example": loaded}},
				worker:       worker,
			}
			prepared, err := model.prepareInstallItems(context.Background(), []installItem{item})
			require.ErrorContains(t, err, "unsupported host requirements for example")
			assert.Contains(t, err.Error(), tt.want)
			assert.Empty(t, prepared.lock.Drivers)
			assert.Zero(t, downloadCalls)
			assert.Zero(t, ensureCalls)

			_, ensureErr := model.ensurePreparedPackage(context.Background(), &item)
			require.ErrorContains(t, ensureErr, "unsupported host requirements for example")
			assert.Zero(t, ensureCalls, "unsupported requirements must be rejected before EnsurePackage")
			after, err := config.GetDriver(cfg, "example")
			require.NoError(t, err)
			assert.Equal(t, loaded.Name, after.Name)
			assert.Equal(t, loaded.Version.String(), after.Version.String())
			assert.Equal(t, libraryPath, after.Driver.Shared.Get(config.PlatformTuple()))
			bytes, err := os.ReadFile(libraryPath)
			require.NoError(t, err)
			assert.Equal(t, sentinel, string(bytes))
			_, err = os.Stat(model.LockFilePath)
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestRegistryAndPathSourcesDoNotFabricateEvidence(t *testing.T) {
	for _, source := range []resolution.SourceSpec{
		{Type: "registry", Reference: "https://registry.example.test"},
		{Type: "path", Reference: "./driver"},
	} {
		t.Run(source.Type, func(t *testing.T) {
			release := testResolvedRelease()
			release.Source = source
			release.Evidence = nil
			entry, err := lockInfoFromResolvedRelease("example", release)
			require.NoError(t, err)
			assert.Empty(t, entry.Evidence)
			assert.Empty(t, entry.resolvedRelease().Evidence)
		})
	}
}

func testResolvedRelease() resolution.ResolvedRelease {
	size := int64(10)
	return resolution.ResolvedRelease{
		DriverID: "example",
		Version:  "1.2.3",
		Source:   resolution.SourceSpec{Type: "packslip", Reference: "github.com/example/driver"},
		Evidence: []resolution.Evidence{{
			Kind:     resolution.EvidenceKindReleaseMetadata,
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/bundle"},
			Hash:     "sha256:" + strings.Repeat("e", 64),
		}},
		Artifacts: []resolution.Artifact{{
			Target: resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu", Variant: "v1"}, Format: "tar.gz", PackageVersion: 2,
			Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/linux.tar.gz"}, Hash: "sha256:" + strings.Repeat("a", 64), Size: &size,
			HostRequirements: resolution.HostRequirements{
				GLibCMin: "2.17", Libs: []string{"libc.so.6"},
				Bins: []resolution.NamedRequirement{{Name: "git", Min: "2.40"}},
			},
		}},
	}
}
