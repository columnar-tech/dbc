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
	"reflect"
	"sort"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/internal/atomicfile"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/columnar-tech/dbc/internal/sourceidentity"
	"github.com/columnar-tech/dbc/internal/sourceresolution"
	"github.com/pelletier/go-toml/v2"
)

var (
	ErrLockedArtifactMissing     = errors.New("locked artifact missing")
	ErrLockRefreshRequired       = errors.New("lock refresh required")
	ErrLockedModeArtifactMissing = errors.New("locked mode artifact missing")
	ErrArtifactAmbiguous         = errors.New("locked artifact selection is ambiguous")
)

func validateLockArtifacts(artifacts []lockArtifact) error {
	seenTargets := make(map[resolution.Target]struct{}, len(artifacts))
	seenLocations := make(map[resolution.ArtifactLocation]lockArtifact, len(artifacts))
	for i, artifact := range artifacts {
		if artifact.PackageVersion != 0 && artifact.PackageVersion != 2 {
			return fmt.Errorf("artifact %d has unsupported dbc package version %d", i, artifact.PackageVersion)
		}
		if err := resolution.ValidateConcreteTarget(artifact.Target); err != nil {
			return fmt.Errorf("artifact %d has invalid target: %w", i, err)
		}
		if canonical := resolution.CanonicalTarget(artifact.Target); canonical != artifact.Target {
			return fmt.Errorf("artifact %d target is not canonical", i)
		}
		if err := resolution.ValidateArtifactLocation(artifact.Location); err != nil {
			return fmt.Errorf("artifact %d has invalid location: %w", i, err)
		}
		if artifact.Hash == "" {
			return fmt.Errorf("artifact %d must have a finalized hash", i)
		}
		if err := validateLockHash(artifact.Hash); err != nil {
			return fmt.Errorf("artifact %d: %w", i, err)
		}
		if artifact.Size != nil && *artifact.Size < 0 {
			return fmt.Errorf("artifact %d has negative size", i)
		}
		if _, exists := seenTargets[artifact.Target]; exists {
			return fmt.Errorf("duplicate artifact target %q", artifactSelectorIdentity(artifact))
		}
		seenTargets[artifact.Target] = struct{}{}
		if prior, exists := seenLocations[artifact.Location]; exists {
			if prior.Hash != artifact.Hash || !compatibleLockSize(prior.Size, artifact.Size) {
				return fmt.Errorf("artifacts sharing location %q have conflicting hash or size", artifact.Location.Value)
			}
			if prior.Size == nil && artifact.Size != nil {
				seenLocations[artifact.Location] = artifact
			}
		} else {
			seenLocations[artifact.Location] = artifact
		}
	}
	return nil
}

func sameLockSize(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func compatibleLockSize(left, right *int64) bool {
	return left == nil || right == nil || *left == *right
}

func validateLockHash(hash string) error {
	// Share the resolution layer's canonical digest contract.
	return resolution.ValidateArtifactMetadata(hash, nil)
}

func artifactSelectorIdentity(artifact lockArtifact) string {
	return targetIdentity(artifact.Target)
}

// LockRefreshRequiredError distinguishes a normal install that can be fixed
// by explicitly refreshing the lock from a locked-mode failure.
type LockRefreshRequiredError struct {
	DriverID string
	Platform string
	Reason   string
}

func (e *LockRefreshRequiredError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("lock file for %s on %s requires refresh: %s", e.DriverID, e.Platform, e.Reason)
	}
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
// Source identity and source-specific artifact requirements are validated by
// sourceresolution before this conversion; load/write boundaries validate the
// complete lock representation.
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
		Evidence: lockEvidenceFromResolution(release.Evidence),
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
		entry.Artifacts = append(entry.Artifacts, lockArtifactFromResolved(artifact))
	}
	return entry, nil
}

func lockArtifactFromResolved(artifact resolution.Artifact) lockArtifact {
	result := lockArtifact{
		Target:           resolution.CanonicalTarget(artifact.Target),
		Format:           artifact.Format,
		PackageVersion:   artifact.PackageVersion,
		Location:         artifact.Location,
		Hash:             artifact.Hash,
		Size:             cloneInt64(artifact.Size),
		HostRequirements: lockHostRequirementsFromResolution(artifact.HostRequirements),
	}
	return result
}

func lockHostRequirementsFromResolution(requirements resolution.HostRequirements) lockHostRequirements {
	result := lockHostRequirements{
		OSMin:    requirements.OSMin,
		GLibCMin: requirements.GLibCMin,
		Libs:     append([]string(nil), requirements.Libs...),
	}
	for _, bin := range requirements.Bins {
		result.Bins = append(result.Bins, lockNamedRequirement{Name: bin.Name, Min: bin.Min})
	}
	return result
}

func resolutionHostRequirementsFromLock(requirements lockHostRequirements) resolution.HostRequirements {
	result := resolution.HostRequirements{
		OSMin:    requirements.OSMin,
		GLibCMin: requirements.GLibCMin,
		Libs:     append([]string(nil), requirements.Libs...),
	}
	for _, bin := range requirements.Bins {
		result.Bins = append(result.Bins, resolution.NamedRequirement{Name: bin.Name, Min: bin.Min})
	}
	return result
}

func cloneLockInfo(entry lockInfo) lockInfo {
	result := entry
	result.Evidence = append([]lockEvidence(nil), entry.Evidence...)
	result.Artifacts = append([]lockArtifact(nil), entry.Artifacts...)
	for i := range result.Artifacts {
		result.Artifacts[i].Size = cloneInt64(entry.Artifacts[i].Size)
		result.Artifacts[i].HostRequirements.Libs = append([]string(nil), entry.Artifacts[i].HostRequirements.Libs...)
		result.Artifacts[i].HostRequirements.Bins = append([]lockNamedRequirement(nil), entry.Artifacts[i].HostRequirements.Bins...)
	}
	if entry.Legacy != nil {
		legacy := *entry.Legacy
		result.Legacy = &legacy
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
		Evidence: resolutionEvidenceFromLock(d.Evidence),
	}
	if d.Version != nil {
		release.Version = d.Version.String()
	}
	for _, artifact := range d.Artifacts {
		resolved := resolution.Artifact{
			Target:           artifact.Target,
			Format:           artifact.Format,
			PackageVersion:   artifact.PackageVersion,
			Location:         artifact.Location,
			Hash:             artifact.Hash,
			Size:             cloneInt64(artifact.Size),
			HostRequirements: resolutionHostRequirementsFromLock(artifact.HostRequirements),
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
	if err := resolution.ValidateEvidence(resolutionEvidenceFromLock(entry.Evidence)); err != nil {
		return fmt.Errorf("driver %q evidence: %w", entry.Name, err)
	}
	if len(entry.Artifacts) == 0 {
		return fmt.Errorf("driver %q has no locked artifacts", entry.Name)
	}
	if err := validateLockArtifacts(entry.Artifacts); err != nil {
		return err
	}
	return nil
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func lockEvidenceFromResolution(evidence []resolution.Evidence) []lockEvidence {
	if len(evidence) == 0 {
		return nil
	}
	result := make([]lockEvidence, len(evidence))
	for i, item := range evidence {
		result[i] = lockEvidence{Kind: item.Kind, Location: item.Location, Hash: item.Hash}
	}
	return result
}

func resolutionEvidenceFromLock(evidence []lockEvidence) []resolution.Evidence {
	if len(evidence) == 0 {
		return nil
	}
	result := make([]resolution.Evidence, len(evidence))
	for i, item := range evidence {
		result[i] = resolution.Evidence{Kind: item.Kind, Location: item.Location, Hash: item.Hash}
	}
	return result
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
	if old.Legacy != nil && samePlatformTarget(old.Legacy.Platform, currentPlatform) {
		if verified == nil || !samePlatformTarget(verified.Platform, old.Legacy.Platform) || verified.LibraryHash != old.Legacy.LibraryHash {
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
	if entry.Legacy == nil || !samePlatformTarget(entry.Legacy.Platform, platform) {
		return nil
	}
	if installedLibraryHash != entry.Legacy.LibraryHash {
		return fmt.Errorf("legacy installed-library checksum mismatch for %s on %s", entry.Name, platform)
	}
	return nil
}

// refreshLockEntry merges metadata for the same source and version. Existing
// artifacts are immutable: a refresh may add a target but cannot replace an
// existing target's location, format, digest, size, or host requirements.
func refreshLockEntry(existing, refreshed lockInfo) (lockInfo, error) {
	if existing.Name != refreshed.Name || !sourceresolution.SameReleaseVersion(
		sourceidentity.Kind(existing.Source.Type), existing.Version, refreshed.Version,
	) {
		return lockInfo{}, errors.New("metadata refresh must keep the locked driver version")
	}
	if !sameLockSourceIdentity(existing.Source, refreshed.Source) {
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
		for index, prior := range merged.Artifacts {
			if artifactSelectorIdentity(prior) != candidateIdentity {
				continue
			}
			found = true
			if prior.Location != candidate.Location || prior.Format != candidate.Format ||
				prior.Hash != candidate.Hash || !sameLockSize(prior.Size, candidate.Size) ||
				(prior.PackageVersion != 0 && candidate.PackageVersion != 0 && prior.PackageVersion != candidate.PackageVersion) ||
				!reflect.DeepEqual(canonicalHostRequirements(prior.HostRequirements), canonicalHostRequirements(candidate.HostRequirements)) {
				return lockInfo{}, fmt.Errorf("metadata refresh contradicts locked artifact %q", candidateIdentity)
			}
			if merged.Artifacts[index].PackageVersion == 0 {
				merged.Artifacts[index].PackageVersion = candidate.PackageVersion
			}
			break
		}
		if !found {
			merged.Artifacts = append(merged.Artifacts, candidate)
		}
	}
	merged.Evidence = canonicalLockEvidence(refreshed.Evidence)
	if existing.Legacy != nil {
		proof := *existing.Legacy
		merged.Legacy = &proof
	}
	if err := validateLockInfo(merged); err != nil {
		return lockInfo{}, err
	}
	return merged, nil
}

func canonicalHostRequirements(requirements lockHostRequirements) lockHostRequirements {
	if len(requirements.Libs) == 0 {
		requirements.Libs = nil
	} else {
		requirements.Libs = append([]string(nil), requirements.Libs...)
		sort.Strings(requirements.Libs)
	}
	if len(requirements.Bins) == 0 {
		requirements.Bins = nil
	} else {
		requirements.Bins = append([]lockNamedRequirement(nil), requirements.Bins...)
		sort.Slice(requirements.Bins, func(i, j int) bool {
			if requirements.Bins[i].Name != requirements.Bins[j].Name {
				return requirements.Bins[i].Name < requirements.Bins[j].Name
			}
			return requirements.Bins[i].Min < requirements.Bins[j].Min
		})
	}
	return requirements
}

// upgradeLockEntry changes the driver version as an explicit operation. It
// never carries a legacy installed-library proof across versions.
func upgradeLockEntry(existing lockInfo, upgraded lockInfo) (lockInfo, error) {
	if existing.Name != upgraded.Name || existing.Version == nil || upgraded.Version == nil {
		return lockInfo{}, errors.New("version upgrade must keep the driver identity")
	}
	if sourceresolution.SameReleaseVersion(sourceidentity.Kind(existing.Source.Type), existing.Version, upgraded.Version) {
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
	target, targetErr := resolution.TargetFromPlatformTuple(platform)
	var matches []lockArtifact
	if targetErr == nil {
		for _, artifact := range entry.Artifacts {
			if artifact.Target == target {
				matches = append(matches, artifact)
			}
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
		result.Drivers[i].Evidence = canonicalLockEvidence(result.Drivers[i].Evidence)
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

func canonicalLockEvidence(evidence []lockEvidence) []lockEvidence {
	result := append([]lockEvidence(nil), evidence...)
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Location.Kind != right.Location.Kind {
			return left.Location.Kind < right.Location.Kind
		}
		if left.Location.Value != right.Location.Value {
			return left.Location.Value < right.Location.Value
		}
		return left.Hash < right.Hash
	})
	return result
}
