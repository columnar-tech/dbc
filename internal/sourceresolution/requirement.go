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

package sourceresolution

import (
	"errors"
	"fmt"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/columnar-tech/dbc/internal/sourceidentity"
)

// SourceSelectionMode identifies how a requirement chooses its source.
type SourceSelectionMode uint8

const (
	SourceSelectionInvalid SourceSelectionMode = iota
	DefaultRegistry
	ExplicitSource
)

// SourceSelection chooses the default registry policy or one canonical,
// explicit source identity. Its fields are private so callers must use the
// validating constructors.
type SourceSelection struct {
	mode SourceSelectionMode
	key  sourceidentity.Key
}

// DefaultRegistrySelection selects any registry source accepted by the
// configured default-registry policy.
func DefaultRegistrySelection() SourceSelection {
	return SourceSelection{mode: DefaultRegistry}
}

// ExplicitSourceSelection binds a requirement to one canonical source key.
func ExplicitSourceSelection(key sourceidentity.Key) (SourceSelection, error) {
	canonical, err := sourceidentity.Parse(key.Kind, key.Reference)
	if err != nil {
		return SourceSelection{}, fmt.Errorf("invalid explicit source identity: %w", err)
	}
	if canonical != key {
		return SourceSelection{}, errors.New("explicit source identity is not canonical")
	}
	return SourceSelection{mode: ExplicitSource, key: key}, nil
}

// Mode reports whether this selection uses the default registry or an
// explicitly named source.
func (selection SourceSelection) Mode() SourceSelectionMode {
	return selection.mode
}

// Key returns the explicit source key, if this is an ExplicitSource selection.
func (selection SourceSelection) Key() (sourceidentity.Key, bool) {
	if selection.mode != ExplicitSource {
		return sourceidentity.Key{}, false
	}
	return selection.key, true
}

// PrereleasePolicy controls whether a registry requirement can match versions
// whose SemVer prerelease field is non-empty.
type PrereleasePolicy uint8

const (
	PrereleaseForbidden PrereleasePolicy = iota
	PrereleaseAllowed
)

// VersionMode identifies the source-specific version rule in a requirement.
type VersionMode uint8

const (
	VersionModeInvalid VersionMode = iota
	RegistryConstraint
	PackslipExact
	PathExact
	PathMetadataDerived
)

// VersionRequirement is a validated source-specific version rule. Its fields
// are private so source and version semantics cannot be changed independently
// after a Requirement is constructed.
type VersionRequirement struct {
	mode       VersionMode
	constraint string
	parsed     *semver.Constraints
	exact      string
	prerelease PrereleasePolicy
}

// RegistryVersionRequirement builds a registry constraint requirement. An
// empty constraint means any version allowed by prereleasePolicy.
func RegistryVersionRequirement(constraint string, prereleasePolicy PrereleasePolicy) (VersionRequirement, error) {
	if prereleasePolicy != PrereleaseForbidden && prereleasePolicy != PrereleaseAllowed {
		return VersionRequirement{}, fmt.Errorf("unsupported prerelease policy %d", prereleasePolicy)
	}
	var parsed *semver.Constraints
	if constraint != "" {
		var err error
		parsed, err = semver.NewConstraint(constraint)
		if err != nil {
			return VersionRequirement{}, fmt.Errorf("invalid registry version constraint %q: %w", constraint, err)
		}
		parsed.IncludePrerelease = prereleasePolicy == PrereleaseAllowed
	}
	return VersionRequirement{
		mode: RegistryConstraint, constraint: constraint, parsed: parsed,
		prerelease: prereleasePolicy,
	}, nil
}

// PackslipVersionRequirement binds a Packslip source to one exact canonical
// SemVer 2.0.0 version. Equality later uses the complete string, including
// build metadata.
func PackslipVersionRequirement(version string) (VersionRequirement, error) {
	if err := validateCanonicalVersion(version); err != nil {
		return VersionRequirement{}, fmt.Errorf("invalid exact Packslip version: %w", err)
	}
	return VersionRequirement{mode: PackslipExact, exact: version}, nil
}

// PathVersionRequirement binds a path source to one exact canonical SemVer
// version.
func PathVersionRequirement(version string) (VersionRequirement, error) {
	if err := validateCanonicalVersion(version); err != nil {
		return VersionRequirement{}, fmt.Errorf("invalid exact path version: %w", err)
	}
	return VersionRequirement{mode: PathExact, exact: version}, nil
}

// PathMetadataVersionRequirement represents a path source whose version is
// taken from the package metadata when the archive is inspected.
func PathMetadataVersionRequirement() VersionRequirement {
	return VersionRequirement{mode: PathMetadataDerived}
}

// Mode reports the version rule used by this requirement.
func (version VersionRequirement) Mode() VersionMode {
	return version.mode
}

// Constraint returns the registry constraint, including an empty string for
// an unconstrained registry requirement.
func (version VersionRequirement) Constraint() (string, bool) {
	if version.mode != RegistryConstraint {
		return "", false
	}
	return version.constraint, true
}

// ExactVersion returns an exact Packslip or path version, when present.
func (version VersionRequirement) ExactVersion() (string, bool) {
	if version.mode != PackslipExact && version.mode != PathExact {
		return "", false
	}
	return version.exact, true
}

// PrereleasePolicy returns the configured registry prerelease policy.
func (version VersionRequirement) PrereleasePolicy() (PrereleasePolicy, bool) {
	if version.mode != RegistryConstraint {
		return PrereleaseForbidden, false
	}
	return version.prerelease, true
}

// Requirement binds one driver ID, source selection, version rule, and
// concrete target. It is immutable after construction.
type Requirement struct {
	driverID string
	source   SourceSelection
	version  VersionRequirement
	target   resolution.Target
}

// NewRequirement validates the source/version combination and target before
// creating a planning requirement.
func NewRequirement(driverID string, source SourceSelection, version VersionRequirement, target resolution.Target) (Requirement, error) {
	if driverID == "" {
		return Requirement{}, errors.New("driver ID is required")
	}
	if source.mode != DefaultRegistry && source.mode != ExplicitSource {
		return Requirement{}, errors.New("source selection is invalid")
	}
	if source.mode == ExplicitSource {
		canonical, err := sourceidentity.Parse(source.key.Kind, source.key.Reference)
		if err != nil || canonical != source.key {
			return Requirement{}, errors.New("explicit source identity is invalid or non-canonical")
		}
	}
	if err := validateSourceVersionPair(source, version); err != nil {
		return Requirement{}, err
	}
	target = resolution.CanonicalTarget(target)
	if err := resolution.ValidateConcreteTarget(target); err != nil {
		return Requirement{}, fmt.Errorf("invalid requirement target: %w", err)
	}
	return Requirement{driverID: driverID, source: source, version: version, target: target}, nil
}

func (requirement Requirement) validate() error {
	if requirement.driverID == "" {
		return errors.New("driver ID is required")
	}
	if requirement.source.mode != DefaultRegistry && requirement.source.mode != ExplicitSource {
		return errors.New("source selection is invalid")
	}
	if requirement.source.mode == ExplicitSource {
		canonical, err := sourceidentity.Parse(requirement.source.key.Kind, requirement.source.key.Reference)
		if err != nil || canonical != requirement.source.key {
			return errors.New("explicit source identity is invalid or non-canonical")
		}
	}
	if err := validateSourceVersionPair(requirement.source, requirement.version); err != nil {
		return err
	}
	switch requirement.version.mode {
	case RegistryConstraint:
		if requirement.version.constraint != "" && requirement.version.parsed == nil {
			return errors.New("registry constraint is invalid")
		}
	case PackslipExact, PathExact:
		if err := validateCanonicalVersion(requirement.version.exact); err != nil {
			return fmt.Errorf("invalid exact version: %w", err)
		}
	case PathMetadataDerived:
	default:
		return errors.New("version requirement is invalid")
	}
	if resolution.CanonicalTarget(requirement.target) != requirement.target {
		return errors.New("requirement target is not canonical")
	}
	if err := resolution.ValidateConcreteTarget(requirement.target); err != nil {
		return fmt.Errorf("invalid requirement target: %w", err)
	}
	return nil
}

func validateSourceVersionPair(source SourceSelection, version VersionRequirement) error {
	var sourceKind sourceidentity.Kind
	if source.mode == ExplicitSource {
		sourceKind = source.key.Kind
	} else {
		sourceKind = sourceidentity.Registry
	}
	switch {
	case sourceKind == sourceidentity.Registry && version.mode == RegistryConstraint:
		return nil
	case sourceKind == sourceidentity.Packslip && version.mode == PackslipExact:
		return nil
	case sourceKind == sourceidentity.Path && (version.mode == PathExact || version.mode == PathMetadataDerived):
		return nil
	default:
		return fmt.Errorf("version mode %d is incompatible with source kind %q", version.mode, sourceKind)
	}
}

// DriverID returns the required resolved driver ID.
func (requirement Requirement) DriverID() string { return requirement.driverID }

// Source returns the validated source selection.
func (requirement Requirement) Source() SourceSelection { return requirement.source }

// Version returns the validated source-specific version rule.
func (requirement Requirement) Version() VersionRequirement { return requirement.version }

// Target returns the canonical concrete target.
func (requirement Requirement) Target() resolution.Target { return requirement.target }

// ValidateResolverResult checks a resolver result before it can be used to
// build an install candidate. It validates source, driver, version, and
// non-finalized release metadata, then returns the artifact for this
// requirement's target so callers do not select it again. A caller persisting
// a lock must still pass the completed release to resolution.ValidateResolvedRelease.
func (requirement Requirement) ValidateResolverResult(release resolution.ResolvedRelease) (resolution.Artifact, error) {
	if err := requirement.validateReleaseIdentity(release); err != nil {
		return resolution.Artifact{}, err
	}
	if err := resolution.ValidateResolvedReleaseCandidate(release); err != nil {
		return resolution.Artifact{}, fmt.Errorf("invalid resolved release: %w", err)
	}
	if err := validatePackslipReleaseArtifacts(release); err != nil {
		return resolution.Artifact{}, err
	}
	artifact, ok := artifactForTarget(release.Artifacts, requirement.target)
	if !ok {
		return resolution.Artifact{}, fmt.Errorf("resolved release has no artifact for target %v", requirement.target)
	}
	return cloneArtifact(artifact), nil
}

func (requirement Requirement) validateReleaseIdentity(release resolution.ResolvedRelease) error {
	if requirement.driverID == "" || requirement.version.mode == VersionModeInvalid {
		return errors.New("requirement is invalid")
	}
	if release.DriverID != requirement.driverID {
		return fmt.Errorf("resolved driver ID %q does not match required driver ID %q", release.DriverID, requirement.driverID)
	}
	if !requirement.matchesSource(release.Source) {
		return fmt.Errorf("resolved source %q identity %q does not match requirement", release.Source.Type, release.Source.Reference)
	}
	if !requirement.version.matches(release.Version) {
		return fmt.Errorf("resolved version %q does not match requirement", release.Version)
	}
	return nil
}

func validatePackslipReleaseArtifacts(release resolution.ResolvedRelease) error {
	if release.Source.Type != string(sourceidentity.Packslip) {
		return nil
	}
	for i, artifact := range release.Artifacts {
		if artifact.PackageVersion != 2 {
			return fmt.Errorf("resolved Packslip artifact %d has unsupported dbc package_version %d", i, artifact.PackageVersion)
		}
	}
	return nil
}

func artifactForTarget(artifacts []resolution.Artifact, target resolution.Target) (resolution.Artifact, bool) {
	for _, artifact := range artifacts {
		if artifact.Target == target {
			return artifact, true
		}
	}
	return resolution.Artifact{}, false
}

func (requirement Requirement) matchesSource(source resolution.SourceSpec) bool {
	key, err := resolvedSourceKey(source)
	if err != nil {
		return false
	}
	switch requirement.source.mode {
	case DefaultRegistry:
		return key.Kind == sourceidentity.Registry
	case ExplicitSource:
		return key == requirement.source.key
	default:
		return false
	}
}

func resolvedSourceKey(source resolution.SourceSpec) (sourceidentity.Key, error) {
	kind := sourceidentity.Kind(source.Type)
	switch kind {
	case sourceidentity.Registry, sourceidentity.Packslip, sourceidentity.Path:
		return sourceidentity.Parse(kind, source.Reference)
	default:
		return sourceidentity.Key{}, fmt.Errorf("unsupported resolved source type %q", source.Type)
	}
}

func (version VersionRequirement) matches(value string) bool {
	parsed, err := semver.StrictNewVersion(value)
	if err != nil || parsed.String() != value {
		return false
	}
	switch version.mode {
	case RegistryConstraint:
		if parsed.Prerelease() != "" && version.prerelease == PrereleaseForbidden {
			return false
		}
		if version.parsed != nil {
			return version.parsed.Check(parsed)
		}
		return parsed.Prerelease() == "" || version.prerelease == PrereleaseAllowed
	case PackslipExact, PathExact:
		return value == version.exact
	case PathMetadataDerived:
		return true
	default:
		return false
	}
}

func validateCanonicalVersion(value string) error {
	parsed, err := semver.StrictNewVersion(value)
	if err != nil {
		return fmt.Errorf("version %q must be an exact SemVer 2.0.0 version: %w", value, err)
	}
	if parsed.String() != value {
		return fmt.Errorf("version %q is not canonical SemVer 2.0.0", value)
	}
	return nil
}
