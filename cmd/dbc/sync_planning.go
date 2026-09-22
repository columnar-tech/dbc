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
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/packslip"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/columnar-tech/dbc/internal/sourceidentity"
	"github.com/columnar-tech/dbc/internal/sourceresolution"
)

type installItem struct {
	Release              resolution.ResolvedRelease
	ArtifactIndex        int
	Platform             string
	InstalledLibraryHash string
	ValidatedLibraryHash string
	LockEntry            *lockInfo
	Archive              *sourceresolution.OpenedArtifact
	Expected             config.ExpectedPackageMetadata
	Validation           *config.PackageValidation
	AlreadyInstalled     *config.DriverInfo
}

type plannedSyncItem struct {
	Name        string
	Spec        driverSpec
	Requirement sourceresolution.Requirement
	Plan        sourceresolution.Plan
	LockEntry   *lockInfo
	LegacyLock  *lockInfo
}

func (item *installItem) selectedArtifact() (*resolution.Artifact, error) {
	if item == nil || item.ArtifactIndex < 0 || item.ArtifactIndex >= len(item.Release.Artifacts) {
		return nil, errors.New("install item has no selected artifact")
	}
	return &item.Release.Artifacts[item.ArtifactIndex], nil
}

func newInstallItem(release resolution.ResolvedRelease, artifactIndex int, platform string, lockEntry *lockInfo) (installItem, error) {
	item := installItem{Release: cloneResolvedReleaseForSync(release), ArtifactIndex: artifactIndex, Platform: platform}
	if artifactIndex < 0 || artifactIndex >= len(release.Artifacts) {
		return installItem{}, errors.New("resolved release has no selected artifact")
	}
	if release.DriverID == "" || release.Version == "" || release.Source.Type == "" || release.Source.Reference == "" {
		return installItem{}, errors.New("resolved release is missing driver, version, or source identity")
	}
	if platform == "" {
		return installItem{}, errors.New("install item is missing its target platform")
	}
	target, err := resolution.TargetFromPlatformTuple(platform)
	if err != nil {
		return installItem{}, fmt.Errorf("invalid install platform %q: %w", platform, err)
	}
	if release.Artifacts[artifactIndex].Target != target {
		return installItem{}, fmt.Errorf("selected artifact target %v does not match platform %q", release.Artifacts[artifactIndex].Target, platform)
	}
	if lockEntry != nil {
		copy := cloneLockInfo(*lockEntry)
		item.LockEntry = &copy
	}
	return item, nil
}

func cloneResolvedReleaseForSync(release resolution.ResolvedRelease) resolution.ResolvedRelease {
	clone := release
	clone.Evidence = append([]resolution.Evidence(nil), release.Evidence...)
	clone.Artifacts = make([]resolution.Artifact, len(release.Artifacts))
	for i, artifact := range release.Artifacts {
		clone.Artifacts[i] = artifact
		clone.Artifacts[i].Size = cloneInt64(artifact.Size)
		clone.Artifacts[i].HostRequirements = cloneHostRequirements(artifact.HostRequirements)
	}
	return clone
}

type preparedSyncMsg struct {
	items []installItem
	lock  LockFile
}

type candidateLockSavedMsg struct{ lock LockFile }

func (s syncModel) planSyncItems(list DriversList) ([]plannedSyncItem, bool, error) {
	planned, needsRegistry, _, err := s.planSyncItemsWithVersion(list)
	return planned, needsRegistry, err
}

// planSyncItemsWithVersion returns the version from the exact lock snapshot
// used for planning. Callers that need migration state must use this method so
// they do not load the lockfile a second time between planning and persisting.
func (s syncModel) planSyncItemsWithVersion(list DriversList) ([]plannedSyncItem, bool, int, error) {
	lf, err := loadLockFile(s.LockFilePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, false, 0, err
	}
	planned, needsRegistry, err := s.planSyncItemsFromLock(list, lf)
	return planned, needsRegistry, lf.Version, err
}

func (s syncModel) planSyncItemsFromLock(list DriversList, lf LockFile) ([]plannedSyncItem, bool, error) {

	planned := make([]plannedSyncItem, 0, len(list.Drivers))
	needsRegistry := false
	for name, spec := range list.Drivers {
		requirement, err := requirementForDriverSpec(name, spec)
		if err != nil {
			return nil, false, err
		}

		entry, hasEntry := lf.lockinfo[name]
		var plan sourceresolution.Plan
		var existing *lockInfo
		var legacy *lockInfo
		if lf.Version == lockFileVersion && hasEntry {
			release := entry.resolvedRelease()
			plan = requirement.Plan(&release, false)
			if plan.Outcome() == sourceresolution.PlanReplay || plan.Outcome() == sourceresolution.PlanRefreshRequired {
				copy := cloneLockInfo(entry)
				existing = &copy
			}
		} else {
			plan = requirement.Plan(nil, false)
			// v1 locks prove only a version and (optionally) installed library
			// bytes. They are migration inputs, never replayable release snapshots.
			// Without source identity, retain that proof only for the historical
			// default-registry selection and verify the selected result later.
			if lf.Version == lockFileVersionV1 && hasEntry &&
				requirement.Source().Mode() == sourceresolution.DefaultRegistry {
				copy := cloneLockInfo(entry)
				legacy = &copy
			}
		}

		switch plan.Outcome() {
		case sourceresolution.PlanReplay:
		case sourceresolution.PlanResolve, sourceresolution.PlanRefreshRequired:
			if requirement.Source().Mode() == sourceresolution.DefaultRegistry {
				needsRegistry = true
			} else if key, ok := requirement.Source().Key(); ok && key.Kind == sourceidentity.Registry {
				needsRegistry = true
			}
		case sourceresolution.PlanLockedArtifactMissing:
			return nil, false, &LockedModeArtifactMissingError{DriverID: name, Platform: config.PlatformTuple()}
		case sourceresolution.PlanReject:
			return nil, false, fmt.Errorf("cannot plan sync for driver %q: %w", name, plan.Err())
		default:
			return nil, false, fmt.Errorf("cannot plan sync for driver %q: invalid plan outcome %d", name, plan.Outcome())
		}
		planned = append(planned, plannedSyncItem{
			Name: name, Spec: spec, Requirement: requirement, Plan: plan,
			LockEntry: existing, LegacyLock: legacy,
		})
	}
	return planned, needsRegistry, nil
}

func requirementForDriverSpec(name string, spec driverSpec) (sourceresolution.Requirement, error) {
	var selection sourceresolution.SourceSelection
	sourceKind := sourceidentity.Registry
	if spec.Source == nil {
		selection = sourceresolution.DefaultRegistrySelection()
	} else {
		key, err := driverSourceIdentity(spec.Source)
		if err != nil {
			return sourceresolution.Requirement{}, fmt.Errorf("driver %q has invalid declared source: %w", name, err)
		}
		selection, err = sourceresolution.ExplicitSourceSelection(key)
		if err != nil {
			return sourceresolution.Requirement{}, fmt.Errorf("driver %q has invalid declared source: %w", name, err)
		}
		sourceKind = key.Kind
	}

	var version sourceresolution.VersionRequirement
	var err error
	switch sourceKind {
	case sourceidentity.Registry:
		constraint := ""
		if spec.Version != nil {
			constraint = spec.Version.String()
		}
		policy := sourceresolution.PrereleaseForbidden
		if spec.Prerelease == "allow" || (spec.Version != nil && spec.Version.IncludePrerelease) {
			policy = sourceresolution.PrereleaseAllowed
		}
		version, err = sourceresolution.RegistryVersionRequirement(constraint, policy)
	case sourceidentity.Packslip:
		if spec.Prerelease != "" {
			return sourceresolution.Requirement{}, fmt.Errorf("driver %q packslip source does not support prerelease policy", name)
		}
		if spec.Version == nil {
			// TODO: Exact versions are required only for the current Packslip PoC.
			// Before Packslip support is generally available, allow omitted versions
			// for GitHub-hosted projects by discovering eligible releases and resolving
			// the latest one to an exact ResolvedRelease. Requirements may then be
			// moving, but ResolvedRelease and dbc.lock must remain exact. Non-GitHub
			// projects can use signed Packslip release lists for version discovery.
			return sourceresolution.Requirement{}, fmt.Errorf("driver %q packslip source requires an exact SemVer 2.0.0 version", name)
		}
		version, err = sourceresolution.PackslipVersionRequirement(spec.Version.String())
	case sourceidentity.Path:
		if spec.Prerelease != "" {
			return sourceresolution.Requirement{}, fmt.Errorf("driver %q path source does not support prerelease policy", name)
		}
		if spec.Version == nil {
			version = sourceresolution.PathMetadataVersionRequirement()
		} else {
			version, err = sourceresolution.PathVersionRequirement(spec.Version.String())
		}
	default:
		return sourceresolution.Requirement{}, fmt.Errorf("source type %q for driver %q is not supported by sync", sourceKind, name)
	}
	if err != nil {
		return sourceresolution.Requirement{}, fmt.Errorf("driver %q has invalid version requirement: %w", name, err)
	}
	target, err := resolution.TargetFromPlatformTuple(config.PlatformTuple())
	if err != nil {
		return sourceresolution.Requirement{}, fmt.Errorf("unsupported sync platform: %w", err)
	}
	return sourceresolution.NewRequirement(name, selection, version, target)
}

func (s syncModel) createInstallList(planned []plannedSyncItem) ([]installItem, error) {
	return s.createInstallListContext(context.Background(), planned)
}

func (s syncModel) createInstallListContext(ctx context.Context, planned []plannedSyncItem) ([]installItem, error) {
	items := make([]installItem, 0, len(planned))
	var packslipResolver packslip.Resolver
	for _, entry := range planned {
		switch entry.Plan.Outcome() {
		case sourceresolution.PlanReplay:
			item, err := installItemFromPlan(entry)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		case sourceresolution.PlanResolve, sourceresolution.PlanRefreshRequired:
			var item installItem
			var err error
			kind := sourceidentity.Registry
			if entry.Requirement.Source().Mode() == sourceresolution.ExplicitSource {
				if key, ok := entry.Requirement.Source().Key(); ok {
					kind = key.Kind
				}
			}
			switch kind {
			case sourceidentity.Registry:
				item, err = s.resolveRegistryPlan(entry)
			case sourceidentity.Packslip:
				if packslipResolver == nil {
					if s.newPackslipResolver == nil {
						return nil, errors.New("no Packslip resolver is configured")
					}
					packslipResolver, err = s.newPackslipResolver()
					if err != nil {
						return nil, fmt.Errorf("create Packslip resolver: %w", err)
					}
				}
				item, err = s.resolvePackslipPlan(ctx, entry, packslipResolver)
			case sourceidentity.Path:
				item, err = s.resolvePathPlan(ctx, entry)
			default:
				err = fmt.Errorf("source type %q for driver %q is not supported by sync", kind, entry.Name)
			}
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		case sourceresolution.PlanReject:
			return nil, fmt.Errorf("cannot plan sync for driver %q: %w", entry.Name, entry.Plan.Err())
		case sourceresolution.PlanLockedArtifactMissing:
			return nil, &LockedModeArtifactMissingError{DriverID: entry.Name, Platform: config.PlatformTuple()}
		default:
			return nil, fmt.Errorf("cannot resolve sync plan for driver %q: invalid outcome %d", entry.Name, entry.Plan.Outcome())
		}
	}
	return items, nil
}

func (s syncModel) resolvePackslipPlan(ctx context.Context, planned plannedSyncItem, resolver packslip.Resolver) (installItem, error) {
	if planned.Spec.Source == nil || planned.Spec.Source.Type != dbc.DriverSourcePackslip {
		return installItem{}, fmt.Errorf("Packslip plan for %q has no Packslip source declaration", planned.Name)
	}
	version := ""
	if exact, ok := planned.Requirement.Version().ExactVersion(); ok {
		version = exact
	}
	var priorLock *lockInfo
	if planned.Plan.Outcome() == sourceresolution.PlanRefreshRequired {
		release, ok := planned.Plan.Release()
		if !ok {
			return installItem{}, fmt.Errorf("Packslip refresh plan for %q has no prior release", planned.Name)
		}
		version = release.Version
		priorLock = planned.LockEntry
	}
	release, err := sourceresolution.ResolvePackslip(ctx, resolver, planned.Spec.Source.Project, planned.Name, version)
	if err != nil {
		return installItem{}, err
	}
	return installItemFromResolverResult(planned.Requirement, release, priorLock)
}

func (s syncModel) resolvePathPlan(ctx context.Context, planned plannedSyncItem) (installItem, error) {
	if planned.Spec.Source == nil || planned.Spec.Source.Type != dbc.DriverSourcePath {
		return installItem{}, fmt.Errorf("path plan for %q has no path source declaration", planned.Name)
	}
	version := ""
	if exact, ok := planned.Requirement.Version().ExactVersion(); ok {
		version = exact
	}
	var priorLock *lockInfo
	var priorVersion string
	if planned.Plan.Outcome() == sourceresolution.PlanRefreshRequired {
		release, ok := planned.Plan.Release()
		if !ok {
			return installItem{}, fmt.Errorf("path refresh plan for %q has no prior release", planned.Name)
		}
		priorVersion = release.Version
		if planned.Requirement.Version().Mode() != sourceresolution.PathMetadataDerived {
			version = release.Version
		}
		priorLock = planned.LockEntry
	}
	baseDir, err := s.projectBaseDir()
	if err != nil {
		return installItem{}, err
	}
	target, err := resolution.TargetFromPlatformTuple(config.PlatformTuple())
	if err != nil {
		return installItem{}, fmt.Errorf("unsupported sync platform: %w", err)
	}
	release, err := sourceresolution.ResolvePath(ctx, planned.Spec.Source.Path, sourceresolution.Request{
		DriverID: planned.Name, Version: version, Target: target,
		Platform: config.PlatformTuple(), BaseDir: baseDir,
	})
	if err != nil {
		return installItem{}, err
	}
	if priorLock != nil && priorVersion != "" && release.Version != priorVersion {
		// An omitted path version follows the current archive metadata. Once it
		// changes, the prior snapshot (including artifacts, evidence, and any
		// legacy proof) no longer describes the candidate release.
		priorLock = nil
	}
	return installItemFromResolverResult(planned.Requirement, release, priorLock)
}

func (s syncModel) projectBaseDir() (string, error) {
	basePath := s.LockFilePath
	if basePath == "" {
		basePath = s.Path
	}
	if basePath == "" {
		return "", errors.New("sync project path is not set")
	}
	baseDir, err := filepath.Abs(filepath.Dir(basePath))
	if err != nil {
		return "", fmt.Errorf("resolve project directory: %w", err)
	}
	return baseDir, nil
}

func installItemFromPlan(planned plannedSyncItem) (installItem, error) {
	release, ok := planned.Plan.Release()
	if !ok {
		return installItem{}, fmt.Errorf("replay plan for %q has no release", planned.Name)
	}
	selected, ok := planned.Plan.Artifact()
	if !ok {
		return installItem{}, fmt.Errorf("replay plan for %q has no selected artifact", planned.Name)
	}
	for i := range release.Artifacts {
		if release.Artifacts[i].Target == selected.Target {
			return newInstallItem(release, i, config.PlatformTuple(), planned.LockEntry)
		}
	}
	return installItem{}, fmt.Errorf("replay plan for %q selected an artifact outside its release", planned.Name)
}

func (s syncModel) resolveRegistryPlan(planned plannedSyncItem) (installItem, error) {
	allowPrerelease := false
	if policy, ok := planned.Requirement.Version().PrereleasePolicy(); ok {
		allowPrerelease = policy == sourceresolution.PrereleaseAllowed
	}
	var exactVersion string
	var priorLock *lockInfo
	refreshingSnapshot := false
	var refreshSource *sourceidentity.Key
	var expectedRefreshSource *sourceidentity.Key
	if planned.Plan.Outcome() == sourceresolution.PlanRefreshRequired {
		release, ok := planned.Plan.Release()
		if !ok {
			return installItem{}, fmt.Errorf("refresh plan for %q has no prior release", planned.Name)
		}
		key, err := sourceidentity.Parse(sourceidentity.Kind(release.Source.Type), release.Source.Reference)
		if err != nil || key.Kind != sourceidentity.Registry {
			return installItem{}, fmt.Errorf("refresh plan for %q has an invalid registry source identity", planned.Name)
		}
		keyCopy := key
		expectedRefreshSource = &keyCopy
		if planned.Requirement.Source().Mode() == sourceresolution.DefaultRegistry {
			// A default-registry requirement normally respects configured registry
			// order. Refreshing a locked release is different: complete that same
			// source snapshot instead of selecting a different registry.
			refreshSource = &key
		}
		exactVersion = release.Version
		refreshingSnapshot = true
		priorLock = planned.LockEntry
	} else if planned.LegacyLock != nil && planned.LegacyLock.Version != nil &&
		planned.Requirement.Source().Mode() == sourceresolution.DefaultRegistry &&
		planned.Requirement.AcceptsVersion(planned.LegacyLock.Version.String()) {
		// A v1 lock has no source identity, so its version may guide registry
		// selection only under the historical default-registry policy.
		exactVersion = planned.LegacyLock.Version.String()
	}
	driver, err := registryDriverForRequirement(planned.Requirement, s.driverIndex, refreshSource)
	if err != nil {
		return installItem{}, wrapWithRegistryContext(err, s.registryErrors)
	}

	var pkg dbc.PkgInfo
	if exactVersion != "" {
		version, err := semver.NewVersion(exactVersion)
		if err != nil {
			return installItem{}, fmt.Errorf("invalid locked version %q for %s: %w", exactVersion, planned.Name, err)
		}
		// Exact versions reach this path only after Requirement accepts them.
		// Ask the legacy registry adapter for that exact prerelease as well;
		// otherwise GetPackage's latest-selection filter would reject a version
		// explicitly selected by the constraint.
		pkg, err = driver.GetPackage(version, config.PlatformTuple(), true)
	} else if constraint, ok := planned.Requirement.Version().Constraint(); ok && constraint != "" {
		parsed, err := semver.NewConstraint(constraint)
		if err != nil {
			return installItem{}, fmt.Errorf("invalid registry constraint %q for %s: %w", constraint, planned.Name, err)
		}
		parsed.IncludePrerelease = allowPrerelease
		pkg, err = driver.GetWithConstraint(parsed, config.PlatformTuple())
	} else {
		pkg, err = driver.GetPackage(nil, config.PlatformTuple(), allowPrerelease)
	}
	if err != nil {
		return installItem{}, err
	}

	release, err := resolvedReleaseFromRegistryPackage(driver, pkg)
	if err != nil {
		return installItem{}, err
	}
	if refreshingSnapshot {
		resolvedSource, sourceErr := sourceidentity.Parse(sourceidentity.Kind(release.Source.Type), release.Source.Reference)
		if sourceErr != nil || resolvedSource.Kind != sourceidentity.Registry {
			return installItem{}, fmt.Errorf("registry refresh for %q returned an invalid source identity", planned.Name)
		}
		if expectedRefreshSource == nil || resolvedSource != *expectedRefreshSource {
			expected := "unknown"
			if expectedRefreshSource != nil {
				expected = expectedRefreshSource.Reference
			}
			return installItem{}, fmt.Errorf("registry refresh for %q returned source %q instead of locked source %q", planned.Name, resolvedSource.Reference, expected)
		}
		lockedVersion, lockErr := semver.NewVersion(exactVersion)
		resolvedVersion, resolveErr := semver.NewVersion(release.Version)
		if lockErr != nil || resolveErr != nil ||
			!sourceresolution.SameReleaseVersion(sourceidentity.Registry, lockedVersion, resolvedVersion) {
			return installItem{}, fmt.Errorf("registry refresh for %q resolved version %q instead of locked version %q", planned.Name, release.Version, exactVersion)
		}
	}
	item, err := installItemFromResolverResult(planned.Requirement, release, priorLock)
	if err != nil {
		return installItem{}, err
	}
	if planned.LegacyLock != nil && planned.LegacyLock.Version != nil && priorLock == nil &&
		planned.Requirement.Source().Mode() == sourceresolution.DefaultRegistry {
		resolvedVersion, versionErr := semver.NewVersion(release.Version)
		if versionErr == nil && sourceresolution.SameReleaseVersion(sourceidentity.Registry, planned.LegacyLock.Version, resolvedVersion) {
			copy := cloneLockInfo(*planned.LegacyLock)
			item.LockEntry = &copy
		}
	}
	return item, nil
}

func registryDriverForRequirement(requirement sourceresolution.Requirement, drivers []dbc.Driver, refreshSource *sourceidentity.Key) (dbc.Driver, error) {
	if requirement.Source().Mode() == sourceresolution.DefaultRegistry && refreshSource == nil {
		return findDriver(requirement.DriverID(), drivers)
	}
	var key sourceidentity.Key
	if requirement.Source().Mode() == sourceresolution.DefaultRegistry {
		if refreshSource == nil || refreshSource.Kind != sourceidentity.Registry {
			return dbc.Driver{}, fmt.Errorf("driver %q has an invalid pinned registry source", requirement.DriverID())
		}
		key = *refreshSource
	} else {
		var ok bool
		key, ok = requirement.Source().Key()
		if !ok || key.Kind != sourceidentity.Registry {
			return dbc.Driver{}, fmt.Errorf("driver %q has an unsupported registry source selection", requirement.DriverID())
		}
	}
	var matches []dbc.Driver
	for _, driver := range drivers {
		if driver.Registry == nil || driver.Registry.BaseURL == nil {
			continue
		}
		candidate, err := sourceidentity.Parse(sourceidentity.Registry, driver.Registry.BaseURL.String())
		if err == nil && candidate == key {
			matches = append(matches, driver)
		}
	}
	driver, err := findDriver(requirement.DriverID(), matches)
	if err != nil {
		if refreshSource != nil {
			return dbc.Driver{}, fmt.Errorf("driver %q was not found in locked registry source %q", requirement.DriverID(), key.Reference)
		}
		return dbc.Driver{}, fmt.Errorf("driver %q was not found in declared registry %q", requirement.DriverID(), key.Reference)
	}
	return driver, nil
}

func resolvedReleaseFromRegistryPackage(driver dbc.Driver, pkg dbc.PkgInfo) (resolution.ResolvedRelease, error) {
	if driver.Path == "" || pkg.Version == nil || strings.TrimSpace(pkg.PlatformTuple) == "" {
		return resolution.ResolvedRelease{}, errors.New("registry package metadata is incomplete")
	}
	if driver.Registry == nil || driver.Registry.BaseURL == nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("driver %q has no registry identity", driver.Path)
	}
	registryKey, err := sourceidentity.Parse(sourceidentity.Registry, driver.Registry.BaseURL.String())
	if err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("driver %q has invalid registry identity: %w", driver.Path, err)
	}
	if pkg.Driver.Path != "" && pkg.Driver.Path != driver.Path {
		return resolution.ResolvedRelease{}, fmt.Errorf("registry resolver returned package for driver %q while resolving %q", pkg.Driver.Path, driver.Path)
	}
	if pkg.Driver.Registry != nil {
		if pkg.Driver.Registry.BaseURL == nil {
			return resolution.ResolvedRelease{}, fmt.Errorf("registry resolver returned package without registry identity for %q", driver.Path)
		}
		packageRegistry, err := sourceidentity.Parse(sourceidentity.Registry, pkg.Driver.Registry.BaseURL.String())
		if err != nil || packageRegistry != registryKey {
			return resolution.ResolvedRelease{}, fmt.Errorf("registry resolver returned package from a different source for %q", driver.Path)
		}
	}
	if pkg.Path == nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("registry package metadata for %q has no archive URL", driver.Path)
	}
	target, err := resolution.TargetFromPlatformTuple(pkg.PlatformTuple)
	if err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("invalid registry package platform %q: %w", pkg.PlatformTuple, err)
	}
	size := cloneInt64(pkg.ArtifactSize)
	release := resolution.ResolvedRelease{
		DriverID: driver.Path,
		Version:  pkg.Version.String(),
		Source:   resolution.SourceSpec{Type: "registry", Reference: registryKey.Reference},
		Artifacts: []resolution.Artifact{{
			Target: target, Format: "tar.gz", Location: resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: pkg.Path.String()},
			Hash: pkg.ArtifactHash, Size: size,
		}},
	}
	if err := resolution.ValidateResolvedReleaseCandidate(release); err != nil {
		return resolution.ResolvedRelease{}, fmt.Errorf("invalid registry package metadata: %w", err)
	}
	return release, nil
}

func installItemFromResolverResult(requirement sourceresolution.Requirement, release resolution.ResolvedRelease, priorLock *lockInfo) (installItem, error) {
	selected, err := requirement.ValidateResolverResult(release)
	if err != nil {
		return installItem{}, fmt.Errorf("source resolver returned an invalid release: %w", err)
	}
	for i := range release.Artifacts {
		if release.Artifacts[i].Target == selected.Target {
			return newInstallItem(release, i, config.PlatformTuple(), priorLock)
		}
	}
	return installItem{}, fmt.Errorf("validated resolver artifact for %q is not part of its release", requirement.DriverID())
}

func installItemFromLockedArtifact(name string, entry lockInfo, artifact lockArtifact) (installItem, error) {
	if err := validateInstallableArtifactFormat(artifact.Format); err != nil {
		return installItem{}, fmt.Errorf("locked artifact for %s: %w", name, err)
	}
	requirements := resolutionHostRequirementsFromLock(artifact.HostRequirements)
	if err := validateHostRequirements(name, requirements); err != nil {
		return installItem{}, err
	}
	if err := resolution.ValidateArtifactLocation(artifact.Location); err != nil {
		return installItem{}, fmt.Errorf("invalid locked artifact location for %s: %w", name, err)
	}
	release := entry.resolvedRelease()
	artifactIndex := -1
	for index := range release.Artifacts {
		if release.Artifacts[index].Target == artifact.Target {
			artifactIndex = index
			break
		}
	}
	if artifactIndex < 0 {
		return installItem{}, fmt.Errorf("locked artifact for %s is not part of its release snapshot", name)
	}
	if !reflect.DeepEqual(lockArtifactFromResolved(release.Artifacts[artifactIndex]), artifact) {
		return installItem{}, fmt.Errorf("locked artifact for %s does not match its release snapshot", name)
	}
	if release.DriverID != name {
		return installItem{}, fmt.Errorf("locked release driver ID %q does not match %q", release.DriverID, name)
	}
	return newInstallItem(release, artifactIndex, config.PlatformTuple(), &entry)
}

func cloneHostRequirements(requirements resolution.HostRequirements) resolution.HostRequirements {
	return resolution.HostRequirements{
		OSMin:    requirements.OSMin,
		GLibCMin: requirements.GLibCMin,
		Libs:     append([]string(nil), requirements.Libs...),
		Bins:     append([]resolution.NamedRequirement(nil), requirements.Bins...),
	}
}
