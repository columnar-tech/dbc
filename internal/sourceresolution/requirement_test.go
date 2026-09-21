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
	"strings"
	"testing"

	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/columnar-tech/dbc/internal/sourceidentity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	planTarget      = resolution.Target{OS: "linux", Arch: "amd64", LibC: "gnu"}
	otherPlanTarget = resolution.Target{OS: "macos", Arch: "arm64"}
)

func TestNewRequirementValidatesSourceAndVersionModes(t *testing.T) {
	registryVersion, err := RegistryVersionRequirement("", PrereleaseForbidden)
	require.NoError(t, err)
	_, err = NewRequirement("driver", DefaultRegistrySelection(), registryVersion, planTarget)
	require.NoError(t, err)

	packslipVersion, err := PackslipVersionRequirement("1.2.3")
	require.NoError(t, err)
	_, err = NewRequirement("driver", DefaultRegistrySelection(), packslipVersion, planTarget)
	require.ErrorContains(t, err, "incompatible")

	key, err := sourceidentity.Parse(sourceidentity.Packslip, "GitHub.com/Example/Driver")
	require.NoError(t, err)
	selection, err := ExplicitSourceSelection(key)
	require.NoError(t, err)
	_, err = NewRequirement("driver", selection, registryVersion, planTarget)
	require.ErrorContains(t, err, "incompatible")
	_, err = NewRequirement("driver", selection, packslipVersion, planTarget)
	require.NoError(t, err)

	_, err = NewRequirement("", selection, packslipVersion, planTarget)
	require.ErrorContains(t, err, "driver ID")
	_, err = NewRequirement("driver", selection, packslipVersion, resolution.Target{OS: "linux"})
	require.ErrorContains(t, err, "architecture")
}

func TestExplicitSourceSelectionRequiresCanonicalKey(t *testing.T) {
	_, err := ExplicitSourceSelection(sourceidentity.Key{Kind: sourceidentity.Packslip, Reference: "GitHub.com/Example/Driver"})
	require.ErrorContains(t, err, "not canonical")

	key, err := sourceidentity.Parse(sourceidentity.Packslip, "GitHub.com/Example/Driver")
	require.NoError(t, err)
	selection, err := ExplicitSourceSelection(key)
	require.NoError(t, err)
	got, ok := selection.Key()
	require.True(t, ok)
	assert.Equal(t, key, got)
}

func TestRegistryVersionRequirementAppliesConstraintAndPrereleasePolicy(t *testing.T) {
	for _, test := range []struct {
		name      string
		policy    PrereleasePolicy
		version   string
		wantMatch bool
	}{
		{name: "stable allowed", policy: PrereleaseForbidden, version: "1.5.0", wantMatch: true},
		{name: "prerelease filtered", policy: PrereleaseForbidden, version: "1.5.0-beta.1"},
		{name: "prerelease allowed", policy: PrereleaseAllowed, version: "1.5.0-beta.1", wantMatch: true},
		{name: "outside constraint", policy: PrereleaseAllowed, version: "2.0.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			version, err := RegistryVersionRequirement(">=1.0.0,<2.0.0", test.policy)
			require.NoError(t, err)
			require.Equal(t, test.wantMatch, version.matches(test.version))
		})
	}
	_, err := RegistryVersionRequirement("not a constraint", PrereleaseForbidden)
	require.Error(t, err)
}

func TestRegistryConstraintCanExplicitlySelectPrerelease(t *testing.T) {
	constraint := ">=1.2.3-beta.1,<1.2.3"
	for _, test := range []struct {
		name      string
		policy    PrereleasePolicy
		wantMatch bool
	}{
		{name: "default policy", policy: PrereleaseForbidden, wantMatch: true},
		{name: "allowed", policy: PrereleaseAllowed, wantMatch: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requirement, err := RegistryVersionRequirement(constraint, test.policy)
			require.NoError(t, err)
			assert.Equal(t, test.wantMatch, requirement.matches("1.2.3-beta.2"))
		})
	}
}

func TestPackslipExactVersionIncludesBuildMetadata(t *testing.T) {
	version, err := PackslipVersionRequirement("1.2.3+build.7")
	require.NoError(t, err)
	assert.True(t, version.matches("1.2.3+build.7"))
	assert.False(t, version.matches("1.2.3+build.8"))
	_, err = PackslipVersionRequirement("v1.2.3")
	require.Error(t, err, "the requirement accepts only canonical versions")
}

func TestPathVersionRequirements(t *testing.T) {
	exact, err := PathVersionRequirement("1.2.3+build.1")
	require.NoError(t, err)
	assert.True(t, exact.matches("1.2.3+build.1"))
	assert.False(t, exact.matches("1.2.3+build.2"))

	derived := PathMetadataVersionRequirement()
	assert.True(t, derived.matches("1.2.3+build.2"))
	assert.False(t, derived.matches("invalid"))
}

func TestRequirementValidateResolvedReleaseChecksSourceDriverVersionAndStructure(t *testing.T) {
	requirement := mustRequirement(t, "driver", DefaultRegistrySelection(), mustRegistryVersion(t, "=1.2.3", PrereleaseForbidden), planTarget)
	release := validPlanRelease("registry", "https://registry.example.test", "1.2.3", "driver", planTarget)
	selected, err := requirement.ValidateResolverResult(release)
	require.NoError(t, err)
	assert.Equal(t, planTarget, selected.Target)

	legacyRegistryCandidate := cloneResolvedRelease(release)
	legacyRegistryCandidate.Artifacts[0].Hash = ""
	legacyRegistryCandidate.Artifacts[0].Size = nil
	_, err = requirement.ValidateResolverResult(legacyRegistryCandidate)
	require.NoError(t, err, "registry candidates can be validated before archive hashing")

	tests := []struct {
		name   string
		change func(*resolution.ResolvedRelease)
		want   string
	}{
		{name: "driver", change: func(r *resolution.ResolvedRelease) { r.DriverID = "other" }, want: "driver ID"},
		{name: "source type", change: func(r *resolution.ResolvedRelease) { r.Source.Type = "packslip" }, want: "source"},
		{name: "source kind", change: func(r *resolution.ResolvedRelease) {
			r.Source = resolution.SourceSpec{Type: "path", Reference: "./driver.tar.gz"}
		}, want: "source"},
		{name: "version", change: func(r *resolution.ResolvedRelease) { r.Version = "1.2.4" }, want: "version"},
		{name: "artifact structure", change: func(r *resolution.ResolvedRelease) { r.Artifacts[0].PackageVersion = 3 }, want: "unsupported dbc package version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneResolvedRelease(release)
			test.change(&candidate)
			_, err := requirement.ValidateResolverResult(candidate)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestRequirementPlanOutcomesAndStaleReleaseIsolation(t *testing.T) {
	requirement := mustRequirement(t, "driver", DefaultRegistrySelection(), mustRegistryVersion(t, "=1.2.3", PrereleaseForbidden), planTarget)
	existing := validPlanRelease("registry", "https://registry.example.test", "1.2.3", "driver", planTarget)

	plan := requirement.Plan(&existing, false)
	assert.Equal(t, PlanReplay, plan.Outcome(), "a valid lock replay does not require a resolver")
	replayed, ok := plan.Release()
	require.True(t, ok)
	assert.Equal(t, existing, replayed)
	selectedArtifact, ok := plan.Artifact()
	require.True(t, ok)
	assert.Equal(t, planTarget, selectedArtifact.Target)
	// The plan gives callers an independent snapshot.
	replayed.Artifacts[0].Hash = "changed"
	stillReplay, _ := plan.Release()
	assert.NotEqual(t, "changed", stillReplay.Artifacts[0].Hash)
	selectedArtifact.Hash = "changed"
	stillSelected, _ := plan.Artifact()
	assert.NotEqual(t, "changed", stillSelected.Hash)

	for _, test := range []struct {
		name   string
		change func(*resolution.ResolvedRelease)
	}{
		{name: "wrong source", change: func(r *resolution.ResolvedRelease) {
			r.Source = resolution.SourceSpec{Type: "path", Reference: "./driver.tar.gz"}
			r.Artifacts[0].Hash = ""
			r.Artifacts[0].Size = nil
		}},
		{name: "wrong version", change: func(r *resolution.ResolvedRelease) { r.Version = "1.2.4" }},
		{name: "wrong driver", change: func(r *resolution.ResolvedRelease) { r.DriverID = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			stale := cloneResolvedRelease(existing)
			test.change(&stale)
			plan := requirement.Plan(&stale, false)
			assert.Equal(t, PlanResolve, plan.Outcome())
			_, hasRelease := plan.Release()
			assert.False(t, hasRelease, "stale artifact and version data must not leak into resolution")
		})
	}

	missingTargetRelease := validPlanRelease("registry", "https://registry.example.test", "1.2.3", "driver", otherPlanTarget)
	refreshPlan := requirement.Plan(&missingTargetRelease, false)
	assert.Equal(t, PlanRefreshRequired, refreshPlan.Outcome())
	retained, ok := refreshPlan.Release()
	require.True(t, ok)
	assert.Equal(t, missingTargetRelease, retained)

	lockedPlan := requirement.Plan(&missingTargetRelease, true)
	assert.Equal(t, PlanLockedArtifactMissing, lockedPlan.Outcome())
	_, ok = lockedPlan.Release()
	assert.False(t, ok)
	assert.NoError(t, lockedPlan.Err())

	_, err := requirement.ValidateResolverResult(missingTargetRelease)
	require.ErrorContains(t, err, "no artifact for target")

	invalidSnapshot := cloneResolvedRelease(existing)
	invalidSnapshot.Artifacts[0].Hash = ""
	invalidPlan := requirement.Plan(&invalidSnapshot, false)
	assert.Equal(t, PlanReject, invalidPlan.Outcome())
	assert.ErrorContains(t, invalidPlan.Err(), "hash and size must either both be present or both be absent")
	_, ok = invalidPlan.Release()
	assert.False(t, ok)
}

func TestDefaultRegistryRejectsNonRegistryRelease(t *testing.T) {
	requirement := mustRequirement(t, "driver", DefaultRegistrySelection(), mustRegistryVersion(t, "", PrereleaseForbidden), planTarget)
	release := validPlanRelease("path", "./driver.tar.gz", "1.2.3", "driver", planTarget)
	_, err := requirement.ValidateResolverResult(release)
	assert.ErrorContains(t, err, "source")
	assert.Equal(t, PlanResolve, requirement.Plan(&release, false).Outcome())
}

func TestDefaultRegistryAcceptsAnyRegistryIdentity(t *testing.T) {
	requirement := mustRequirement(t, "driver", DefaultRegistrySelection(), mustRegistryVersion(t, "=1.2.3", PrereleaseForbidden), planTarget)
	release := validPlanRelease("registry", "https://another-registry.example.test", "1.2.3", "driver", planTarget)
	_, err := requirement.ValidateResolverResult(release)
	require.NoError(t, err)
}

func TestExplicitSourceAcceptsOnlyExactCanonicalKey(t *testing.T) {
	key, err := sourceidentity.Parse(sourceidentity.Packslip, "GitHub.com/Example/Driver")
	require.NoError(t, err)
	selection, err := ExplicitSourceSelection(key)
	require.NoError(t, err)
	requirement := mustRequirement(t, "driver", selection, mustPackslipVersion(t, "1.2.3+meta"), planTarget)

	matching := validPlanRelease("packslip", "github.com/example/driver", "1.2.3+meta", "driver", planTarget)
	_, err = requirement.ValidateResolverResult(matching)
	assert.NoError(t, err)

	differentSource := cloneResolvedRelease(matching)
	differentSource.Source.Reference = "github.com/example/other"
	_, err = requirement.ValidateResolverResult(differentSource)
	assert.ErrorContains(t, err, "source")

	differentBuild := cloneResolvedRelease(matching)
	differentBuild.Version = "1.2.3+other"
	_, err = requirement.ValidateResolverResult(differentBuild)
	assert.ErrorContains(t, err, "version")

	missingMarker := cloneResolvedRelease(matching)
	missingMarker.Artifacts[0].PackageVersion = 0
	_, err = requirement.ValidateResolverResult(missingMarker)
	assert.ErrorContains(t, err, "package_version")
	for _, locked := range []bool{false, true} {
		plan := requirement.Plan(&missingMarker, locked)
		assert.Equal(t, PlanReject, plan.Outcome())
		assert.ErrorContains(t, plan.Err(), "package_version")
		_, hasRelease := plan.Release()
		assert.False(t, hasRelease, "invalid Packslip artifacts are not refresh inputs")
		_, hasArtifact := plan.Artifact()
		assert.False(t, hasArtifact, "invalid Packslip artifacts are never executable")
	}
	newVersionRequirement := mustRequirement(t, "driver", selection, mustPackslipVersion(t, "1.2.4"), planTarget)
	assert.Equal(t, PlanReject, newVersionRequirement.Plan(&missingMarker, false).Outcome(),
		"an invalid Packslip snapshot cannot be healed by changing the requested version")
}

func mustRequirement(t *testing.T, driverID string, source SourceSelection, version VersionRequirement, target resolution.Target) Requirement {
	t.Helper()
	requirement, err := NewRequirement(driverID, source, version, target)
	require.NoError(t, err)
	return requirement
}

func mustRegistryVersion(t *testing.T, constraint string, policy PrereleasePolicy) VersionRequirement {
	t.Helper()
	version, err := RegistryVersionRequirement(constraint, policy)
	require.NoError(t, err)
	return version
}

func mustPackslipVersion(t *testing.T, value string) VersionRequirement {
	t.Helper()
	version, err := PackslipVersionRequirement(value)
	require.NoError(t, err)
	return version
}

func validPlanRelease(sourceType, reference, version, driverID string, target resolution.Target) resolution.ResolvedRelease {
	size := int64(1)
	packageVersion := 0
	if sourceType == "packslip" {
		packageVersion = 2
	}
	return resolution.ResolvedRelease{
		DriverID: driverID,
		Version:  version,
		Source:   resolution.SourceSpec{Type: sourceType, Reference: reference},
		Artifacts: []resolution.Artifact{{
			Target:         target,
			Format:         "tar.gz",
			PackageVersion: packageVersion,
			Location:       resolution.ArtifactLocation{Kind: resolution.ArtifactLocationURL, Value: "https://example.test/driver.tar.gz"},
			Hash:           "sha256:" + strings.Repeat("a", 64),
			Size:           &size,
		}},
	}
}
