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
	"errors"
	"fmt"
	"sort"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/internal/atomicfile"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/pelletier/go-toml/v2"
)

var (
	ErrLockedArtifactMissing     = errors.New("locked artifact missing")
	ErrLockRefreshRequired       = errors.New("lock refresh required")
	ErrLockedModeArtifactMissing = errors.New("locked mode artifact missing")
	ErrArtifactAmbiguous         = errors.New("locked artifact selection is ambiguous")
)

// LockRefreshRequiredError distinguishes a normal install that can be fixed
// by explicitly refreshing the lock from a locked-mode failure.
type LockRefreshRequiredError struct {
	DriverID string
	Platform string
}

func (e *LockRefreshRequiredError) Error() string {
	return fmt.Sprintf("lock file has no artifact for %s on %s; refresh the lock file explicitly", e.DriverID, e.Platform)
}

func (e *LockRefreshRequiredError) Is(target error) bool {
	return target == ErrLockRefreshRequired || target == ErrLockedArtifactMissing
}

type LockedModeArtifactMissingError struct {
	DriverID string
	Platform string
}

func (e *LockedModeArtifactMissingError) Error() string {
	return fmt.Sprintf("%s for %s on %s in locked mode", ErrLockedArtifactMissing, e.DriverID, e.Platform)
}

func (e *LockedModeArtifactMissingError) Is(target error) bool {
	return target == ErrLockedModeArtifactMissing || target == ErrLockedArtifactMissing
}

// VerifiedLegacyLibrary is evidence from the installed shared library itself.
// It must never be populated from an archive hash.
type VerifiedLegacyLibrary struct {
	Platform    string
	LibraryHash string
}

// lockInfoFromResolvedRelease is the snapshot boundary: a lock entry may only
// be created from a source-resolved release with finalized archive metadata.
func lockInfoFromResolvedRelease(name string, release resolution.ResolvedRelease) (lockInfo, error) {
	var entry lockInfo
	if name == "" || release.DriverID != name {
		return entry, fmt.Errorf("resolved release driver ID %q does not match lock entry %q", release.DriverID, name)
	}
	if err := resolution.ValidateResolvedRelease(release); err != nil {
		return entry, fmt.Errorf("cannot snapshot unresolved release: %w", err)
	}
	version, err := semver.NewVersion(release.Version)
	if err != nil {
		return entry, fmt.Errorf("invalid resolved version %q: %w", release.Version, err)
	}
	entry = lockInfo{
		Name:    name,
		Version: version,
		Source: lockSource{
			Type: release.Source.Type,
		},
		Evidence: lockEvidence{
			BundleURL:       release.Evidence.BundleURL,
			BundleHash:      release.Evidence.BundleHash,
			ReleaseListURL:  release.Evidence.ReleaseListURL,
			ReleaseListHash: release.Evidence.ReleaseListHash,
		},
	}
	switch release.Source.Type {
	case "packslip":
		entry.Source.Project = release.Source.Reference
	case "registry":
		entry.Source.URL = release.Source.Reference
	case "path":
		entry.Source.Path = release.Source.Reference
	default:
		return lockInfo{}, fmt.Errorf("unsupported resolved source type %q", release.Source.Type)
	}
	for _, artifact := range release.Artifacts {
		lockedArtifact := lockArtifactFromResolved(artifact)
		if release.Source.Type == "path" {
			lockedArtifact.Path = lockedArtifact.URL
			lockedArtifact.URL = ""
		}
		entry.Artifacts = append(entry.Artifacts, lockedArtifact)
	}
	if err := validateLockInfo(entry); err != nil {
		return lockInfo{}, err
	}
	return entry, nil
}

func lockArtifactFromResolved(artifact resolution.Artifact) lockArtifact {
	result := lockArtifact{
		Platform: artifact.Platform,
		OS:       artifact.OS,
		Arch:     artifact.Arch,
		LibC:     artifact.LibC,
		Variant:  artifact.Variant,
		Format:   artifact.Format,
		URL:      artifact.URL,
		Hash:     artifact.Hash,
		Size:     cloneInt64(artifact.Size),
		HostRequirements: lockHostRequirements{
			OSMin:    artifact.HostRequirements.OSMin,
			GLibCMin: artifact.HostRequirements.GLibCMin,
			Libs:     append([]string(nil), artifact.HostRequirements.Libs...),
		},
	}
	for _, bin := range artifact.HostRequirements.Bins {
		result.HostRequirements.Bins = append(result.HostRequirements.Bins, lockNamedRequirement{Name: bin.Name, Min: bin.Min})
	}
	return result
}

func (d lockInfo) resolvedRelease() resolution.ResolvedRelease {
	sourceReference := ""
	switch d.Source.Type {
	case "packslip":
		sourceReference = d.Source.Project
	case "registry":
		sourceReference = d.Source.URL
	case "path":
		sourceReference = d.Source.Path
	}
	release := resolution.ResolvedRelease{
		DriverID: d.Name,
		Source:   resolution.SourceSpec{Type: d.Source.Type, Reference: sourceReference},
		Evidence: resolution.Evidence{
			BundleURL:       d.Evidence.BundleURL,
			BundleHash:      d.Evidence.BundleHash,
			ReleaseListURL:  d.Evidence.ReleaseListURL,
			ReleaseListHash: d.Evidence.ReleaseListHash,
		},
	}
	if d.Version != nil {
		release.Version = d.Version.String()
	}
	for _, artifact := range d.Artifacts {
		artifactURL := artifact.URL
		if artifact.Path != "" {
			artifactURL = artifact.Path
		}
		resolved := resolution.Artifact{
			Platform: artifact.Platform,
			OS:       artifact.OS,
			Arch:     artifact.Arch,
			LibC:     artifact.LibC,
			Variant:  artifact.Variant,
			Format:   artifact.Format,
			URL:      artifactURL,
			Hash:     artifact.Hash,
			Size:     cloneInt64(artifact.Size),
			HostRequirements: resolution.HostRequirements{
				OSMin:    artifact.HostRequirements.OSMin,
				GLibCMin: artifact.HostRequirements.GLibCMin,
				Libs:     append([]string(nil), artifact.HostRequirements.Libs...),
			},
		}
		for _, bin := range artifact.HostRequirements.Bins {
			resolved.HostRequirements.Bins = append(resolved.HostRequirements.Bins, resolution.NamedRequirement{Name: bin.Name, Min: bin.Min})
		}
		release.Artifacts = append(release.Artifacts, resolved)
	}
	return release
}

func validateLockInfo(entry lockInfo) error {
	if entry.Name == "" {
		return errors.New("lock entry has no driver name")
	}
	if entry.Version == nil {
		return fmt.Errorf("driver %q has no version", entry.Name)
	}
	if entry.Legacy != nil {
		if entry.Legacy.Platform == "" {
			return fmt.Errorf("driver %q legacy proof has no platform", entry.Name)
		}
		if err := validateLegacyLibraryHash(entry.Legacy.LibraryHash); err != nil {
			return fmt.Errorf("driver %q legacy proof: %w", entry.Name, err)
		}
	}
	if err := validateLockSource(entry.Source); err != nil {
		return fmt.Errorf("driver %q source: %w", entry.Name, err)
	}
	if (entry.Evidence.BundleURL == "") != (entry.Evidence.BundleHash == "") {
		return fmt.Errorf("driver %q evidence must include both bundle URL and hash", entry.Name)
	}
	if entry.Evidence.BundleURL != "" {
		if err := validateHTTPURL("bundle URL", entry.Evidence.BundleURL); err != nil {
			return fmt.Errorf("driver %q evidence: %w", entry.Name, err)
		}
	}
	if (entry.Evidence.ReleaseListURL == "") != (entry.Evidence.ReleaseListHash == "") {
		return fmt.Errorf("driver %q evidence must include both release-list URL and hash", entry.Name)
	}
	if entry.Evidence.ReleaseListURL != "" {
		if err := validateHTTPURL("release-list URL", entry.Evidence.ReleaseListURL); err != nil {
			return fmt.Errorf("driver %q evidence: %w", entry.Name, err)
		}
	}
	for _, hash := range []string{entry.Evidence.BundleHash, entry.Evidence.ReleaseListHash} {
		if hash != "" {
			if err := validateLockHash(hash); err != nil {
				return fmt.Errorf("driver %q evidence: %w", entry.Name, err)
			}
		}
	}
	if len(entry.Artifacts) == 0 {
		return fmt.Errorf("driver %q has no locked artifacts", entry.Name)
	}
	return validateLockArtifacts(entry.Artifacts)
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// migrateV1Entry upgrades metadata without changing the resolved version.
// If this machine currently uses the legacy platform, the installed-library
// checksum must be explicitly re-verified before producing a v2 snapshot.
func migrateV1Entry(old lockInfo, release resolution.ResolvedRelease, currentPlatform string, verified *VerifiedLegacyLibrary) (lockInfo, error) {
	if old.Version == nil {
		return lockInfo{}, fmt.Errorf("v1 lock entry %q has no version", old.Name)
	}
	if old.Name != release.DriverID || release.Version != old.Version.String() {
		return lockInfo{}, fmt.Errorf("migration must preserve driver %q version %s", old.Name, old.Version)
	}
	if old.Legacy != nil && old.Legacy.Platform == currentPlatform {
		if verified == nil || verified.Platform != old.Legacy.Platform || verified.LibraryHash != old.Legacy.LibraryHash {
			return lockInfo{}, fmt.Errorf("cannot migrate %s on %s without a matching verified installed-library hash", old.Name, currentPlatform)
		}
	}
	entry, err := lockInfoFromResolvedRelease(old.Name, release)
	if err != nil {
		return lockInfo{}, err
	}
	if old.Legacy != nil {
		proof := *old.Legacy
		entry.Legacy = &proof
	}
	return entry, nil
}

// verifyLegacyLibraryProof checks the installed library against the v1 proof
// when an artifact for its original platform is used after migration.
func verifyLegacyLibraryProof(entry lockInfo, platform, installedLibraryHash string) error {
	if entry.Legacy == nil || entry.Legacy.Platform != platform {
		return nil
	}
	if installedLibraryHash != entry.Legacy.LibraryHash {
		return fmt.Errorf("legacy installed-library checksum mismatch for %s on %s", entry.Name, platform)
	}
	return nil
}

// refreshLockEntry merges metadata for the same source and version. Existing
// artifacts are immutable: a refresh may add a target but cannot replace an
// artifact whose digest or size contradicts the current lock.
func refreshLockEntry(existing, refreshed lockInfo) (lockInfo, error) {
	if existing.Name != refreshed.Name || existing.Version == nil || refreshed.Version == nil || !existing.Version.Equal(refreshed.Version) {
		return lockInfo{}, errors.New("metadata refresh must keep the locked driver version")
	}
	if existing.Source != refreshed.Source {
		return lockInfo{}, errors.New("metadata refresh must keep the locked source identity")
	}
	if err := validateLockInfo(existing); err != nil {
		return lockInfo{}, fmt.Errorf("existing lock entry: %w", err)
	}
	if err := validateLockInfo(refreshed); err != nil {
		return lockInfo{}, fmt.Errorf("refreshed lock entry: %w", err)
	}
	merged := existing
	merged.Artifacts = append([]lockArtifact(nil), existing.Artifacts...)
	for _, candidate := range refreshed.Artifacts {
		candidateIdentity := artifactSelectorIdentity(candidate)
		found := false
		for _, prior := range merged.Artifacts {
			if artifactSelectorIdentity(prior) != candidateIdentity {
				continue
			}
			found = true
			if prior.Hash != candidate.Hash || *prior.Size != *candidate.Size {
				return lockInfo{}, fmt.Errorf("metadata refresh contradicts locked artifact %q", candidateIdentity)
			}
			break
		}
		if !found {
			merged.Artifacts = append(merged.Artifacts, candidate)
		}
	}
	merged.Evidence = refreshed.Evidence
	if existing.Legacy != nil {
		proof := *existing.Legacy
		merged.Legacy = &proof
	}
	if err := validateLockInfo(merged); err != nil {
		return lockInfo{}, err
	}
	return merged, nil
}

// upgradeLockEntry changes the driver version as an explicit operation. It
// never carries a legacy installed-library proof across versions.
func upgradeLockEntry(existing lockInfo, upgraded lockInfo) (lockInfo, error) {
	if existing.Name != upgraded.Name || existing.Version == nil || upgraded.Version == nil {
		return lockInfo{}, errors.New("version upgrade must keep the driver identity")
	}
	if existing.Version.Equal(upgraded.Version) {
		return lockInfo{}, errors.New("version upgrade requires a different version")
	}
	if upgraded.Legacy != nil {
		return lockInfo{}, errors.New("version upgrade cannot carry a legacy library proof")
	}
	if err := validateLockInfo(upgraded); err != nil {
		return lockInfo{}, err
	}
	return upgraded, nil
}

// selectLockedArtifact performs pure target selection from lock data. It does
// not consult a registry, a release API, or persistent trust state.
func selectLockedArtifact(entry lockInfo, platform string, locked bool) (lockArtifact, error) {
	var matches []lockArtifact
	for _, artifact := range entry.Artifacts {
		if artifactMatchesPlatform(artifact, platform) {
			matches = append(matches, artifact)
		}
	}
	if len(matches) > 1 {
		return lockArtifact{}, fmt.Errorf("%w for %s on %s", ErrArtifactAmbiguous, entry.Name, platform)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if locked {
		return lockArtifact{}, &LockedModeArtifactMissingError{DriverID: entry.Name, Platform: platform}
	}
	return lockArtifact{}, &LockRefreshRequiredError{DriverID: entry.Name, Platform: platform}
}

func artifactMatchesPlatform(artifact lockArtifact, target string) bool {
	targetOS, targetArch, targetLibC, targetVariant, ok := parsePlatformTuple(target)
	if !ok {
		return false
	}
	artifactOS, artifactArch, artifactLibC, artifactVariant, parsed := parsePlatformTuple(artifact.Platform)
	if !parsed {
		artifactOS, artifactArch, artifactLibC, artifactVariant = artifact.OS, artifact.Arch, "", ""
	}
	if artifact.OS != "" {
		artifactOS = artifact.OS
	}
	if artifact.Arch != "" {
		artifactArch = artifact.Arch
	}
	if artifact.LibC != "" {
		artifactLibC = artifact.LibC
	}
	if artifact.Variant != "" {
		artifactVariant = artifact.Variant
	}
	if artifactOS != targetOS || artifactArch != targetArch {
		return false
	}
	// Existing dbc Linux tuples mean GNU by default; a musl build must not
	// silently satisfy an ordinary linux_amd64 target.
	if targetOS == "linux" && targetLibC == "" {
		targetLibC = "gnu"
	}
	if targetOS == "linux" && artifactLibC == "" {
		artifactLibC = "gnu"
	}
	if artifactLibC != targetLibC {
		return false
	}
	if artifactVariant != "" && artifactVariant != targetVariant {
		return false
	}
	return true
}

func writeLockFileAtomic(path string, lock LockFile) error {
	if lock.Version != 0 && lock.Version != lockFileVersion {
		return fmt.Errorf("cannot write lock file version %d", lock.Version)
	}
	canonical := canonicalLockFile(lock)
	canonical.Version = lockFileVersion
	if err := validateLockFileV2(canonical); err != nil {
		return fmt.Errorf("cannot write invalid lock file: %w", err)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(lockFileV2(canonical)); err != nil {
		return fmt.Errorf("failed to encode lock file: %w", err)
	}
	if err := atomicfile.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("failed to atomically write lock file %s: %w", path, err)
	}
	return nil
}

func canonicalLockFile(lock LockFile) LockFile {
	result := lock
	result.Version = lockFileVersion
	result.Drivers = append([]lockInfo(nil), lock.Drivers...)
	result.lockinfo = nil
	sort.Slice(result.Drivers, func(i, j int) bool { return result.Drivers[i].Name < result.Drivers[j].Name })
	for i := range result.Drivers {
		result.Drivers[i].Artifacts = append([]lockArtifact(nil), result.Drivers[i].Artifacts...)
		sort.Slice(result.Drivers[i].Artifacts, func(a, b int) bool {
			return artifactSelectorIdentity(result.Drivers[i].Artifacts[a]) < artifactSelectorIdentity(result.Drivers[i].Artifacts[b])
		})
		for j := range result.Drivers[i].Artifacts {
			result.Drivers[i].Artifacts[j].HostRequirements.Libs = append([]string(nil), result.Drivers[i].Artifacts[j].HostRequirements.Libs...)
			sort.Strings(result.Drivers[i].Artifacts[j].HostRequirements.Libs)
			result.Drivers[i].Artifacts[j].HostRequirements.Bins = append([]lockNamedRequirement(nil), result.Drivers[i].Artifacts[j].HostRequirements.Bins...)
			sort.Slice(result.Drivers[i].Artifacts[j].HostRequirements.Bins, func(x, y int) bool {
				left, right := result.Drivers[i].Artifacts[j].HostRequirements.Bins[x], result.Drivers[i].Artifacts[j].HostRequirements.Bins[y]
				if left.Name != right.Name {
					return left.Name < right.Name
				}
				return left.Min < right.Min
			})
		}
	}
	return result
}
