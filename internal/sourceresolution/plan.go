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

	"github.com/columnar-tech/dbc/internal/resolution"
)

// PlanOutcome describes the I/O-free action needed for one requirement.
type PlanOutcome uint8

const (
	PlanOutcomeUnspecified PlanOutcome = iota
	PlanResolve
	PlanReplay
	PlanRefreshRequired
	PlanLockedArtifactMissing
	PlanReject
)

// Plan is an immutable decision for one requirement and optional existing
// source-neutral release snapshot. Invalid existing input produces PlanReject
// with an explanation available from Err.
type Plan struct {
	outcome  PlanOutcome
	reason   string
	release  *resolution.ResolvedRelease
	artifact *resolution.Artifact
}

// Outcome returns the action selected by the plan.
func (plan Plan) Outcome() PlanOutcome { return plan.outcome }

// Err returns the validation error for PlanReject outcomes.
func (plan Plan) Err() error {
	if plan.outcome != PlanReject || plan.reason == "" {
		return nil
	}
	return errors.New(plan.reason)
}

// Release returns a copy of the validated existing snapshot retained by a
// replay or an unlocked refresh plan. Resolve plans never expose stale source,
// version, artifact, or evidence data.
func (plan Plan) Release() (resolution.ResolvedRelease, bool) {
	if plan.release == nil {
		return resolution.ResolvedRelease{}, false
	}
	return cloneResolvedRelease(*plan.release), true
}

// Artifact returns the selected concrete target artifact for a replay plan.
// Other outcomes do not expose an artifact for execution.
func (plan Plan) Artifact() (resolution.Artifact, bool) {
	if plan.artifact == nil {
		return resolution.Artifact{}, false
	}
	return cloneArtifact(*plan.artifact), true
}

// Plan classifies the existing release for the requirement's target. When
// locked is true, an otherwise reusable snapshot with no artifact for the
// target produces PlanLockedArtifactMissing for translation to the CLI's
// locked-mode error. A source, driver, or version mismatch in a valid snapshot
// produces PlanResolve with no reference to the old snapshot.
func (requirement Requirement) Plan(existing *resolution.ResolvedRelease, locked bool) Plan {
	if err := requirement.validate(); err != nil {
		return Plan{outcome: PlanReject, reason: "invalid requirement: " + err.Error()}
	}
	if existing == nil {
		return Plan{outcome: PlanResolve}
	}
	if err := validateExistingIdentity(*existing); err != nil {
		return Plan{outcome: PlanReject, reason: "invalid existing release identity: " + err.Error()}
	}
	if err := requirement.validateReleaseIdentity(*existing); err != nil {
		return Plan{outcome: PlanResolve}
	}
	if err := resolution.ValidateResolvedRelease(*existing); err != nil {
		return Plan{outcome: PlanReject, reason: "invalid existing release: " + err.Error()}
	}
	artifact, ok := artifactForTarget(existing.Artifacts, requirement.target)
	if !ok {
		if locked {
			return Plan{outcome: PlanLockedArtifactMissing}
		}
		release := cloneResolvedRelease(*existing)
		return Plan{outcome: PlanRefreshRequired, release: &release}
	}
	release := cloneResolvedRelease(*existing)
	selected := cloneArtifact(artifact)
	return Plan{outcome: PlanReplay, release: &release, artifact: &selected}
}

func validateExistingIdentity(release resolution.ResolvedRelease) error {
	if release.DriverID == "" {
		return errors.New("driver ID is empty")
	}
	if err := validateCanonicalVersion(release.Version); err != nil {
		return fmt.Errorf("invalid version: %w", err)
	}
	if _, err := resolvedSourceKey(release.Source); err != nil {
		return fmt.Errorf("invalid source identity: %w", err)
	}
	return nil
}

func cloneResolvedRelease(release resolution.ResolvedRelease) resolution.ResolvedRelease {
	clone := release
	clone.Evidence = append([]resolution.Evidence(nil), release.Evidence...)
	clone.Artifacts = make([]resolution.Artifact, len(release.Artifacts))
	for i, artifact := range release.Artifacts {
		clone.Artifacts[i] = cloneArtifact(artifact)
	}
	return clone
}

func cloneArtifact(artifact resolution.Artifact) resolution.Artifact {
	artifact.Size = cloneInt64(artifact.Size)
	artifact.HostRequirements = cloneHostRequirements(artifact.HostRequirements)
	return artifact
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneHostRequirements(requirements resolution.HostRequirements) resolution.HostRequirements {
	clone := requirements
	clone.Libs = append([]string(nil), requirements.Libs...)
	clone.Bins = append([]resolution.NamedRequirement(nil), requirements.Bins...)
	return clone
}
