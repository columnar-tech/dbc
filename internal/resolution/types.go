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

// Package resolution contains the shared, module-internal source resolution
// model. Keeping these types under internal prevents them from becoming part
// of dbc's public Go API while allowing the CLI and library to share them.
package resolution

import (
	"fmt"
	"strings"
)

// Target identifies one concrete operating-system and architecture target.
// Empty variant denotes the ordinary variant; it is never a wildcard.
type Target struct {
	OS      string `toml:"os" json:"os"`
	Arch    string `toml:"arch" json:"arch"`
	LibC    string `toml:"libc,omitempty" json:"libc,omitempty"`
	Variant string `toml:"variant,omitempty" json:"variant,omitempty"`
}

// CanonicalTarget applies the small set of established aliases at adapter
// boundaries. Unknown values are preserved rather than guessed.
func CanonicalTarget(target Target) Target {
	switch target.OS {
	case "darwin":
		target.OS = "macos"
	}
	switch target.Arch {
	case "x86_64":
		target.Arch = "amd64"
	case "aarch64":
		target.Arch = "arm64"
	}
	if target.OS == "linux" && target.LibC == "" {
		target.LibC = "gnu"
	}
	return target
}

// TargetFromPlatformTuple is a one-way adapter from the CLI/registry tuple
// grammar to a canonical target: the tuple carries OS, architecture, the known
// gnu/musl libc tokens, and optional variant components. It does not infer an
// unknown libc or promise that arbitrary Target values can be encoded back into
// a tuple. Callers that need the original tuple for URL construction must keep
// it separately.
func TargetFromPlatformTuple(platform string) (Target, error) {
	if platform == "" {
		return Target{}, fmt.Errorf("platform tuple is empty")
	}
	parts := strings.Split(platform, "_")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return Target{}, fmt.Errorf("platform tuple %q must contain OS and architecture", platform)
	}
	archEnd := 2
	arch := parts[1]
	if len(parts) > 2 && parts[1] == "x86" && parts[2] == "64" {
		arch = "x86_64"
		archEnd = 3
	}
	target := Target{OS: parts[0], Arch: arch}
	for _, part := range parts[archEnd:] {
		if part == "" {
			return Target{}, fmt.Errorf("platform tuple %q contains an empty component", platform)
		}
		switch part {
		case "gnu", "musl":
			if target.LibC != "" {
				return Target{}, fmt.Errorf("platform tuple %q contains multiple libc components", platform)
			}
			target.LibC = part
		default:
			if target.Variant == "" {
				target.Variant = part
			} else {
				target.Variant += "_" + part
			}
		}
	}
	target = CanonicalTarget(target)
	if err := ValidateConcreteTarget(target); err != nil {
		return Target{}, fmt.Errorf("platform tuple %q: %w", platform, err)
	}
	return target, nil
}

// ValidateConcreteTarget rejects selectors that cannot identify a concrete
// target. Token values are intentionally not restricted to a known platform
// inventory so new platforms can be represented without guessing semantics.
func ValidateConcreteTarget(target Target) error {
	for _, selector := range []struct{ field, value string }{
		{field: "OS", value: target.OS},
		{field: "architecture", value: target.Arch},
		{field: "libc", value: target.LibC},
		{field: "variant", value: target.Variant},
	} {
		field, value := selector.field, selector.value
		if value == "" {
			if field == "OS" || field == "architecture" {
				return fmt.Errorf("target %s is empty", field)
			}
			continue
		}
		if !validTargetToken(value) {
			return fmt.Errorf("target %s %q is invalid", field, value)
		}
	}
	return nil
}

func validTargetToken(value string) bool {
	if value == "" || !((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= '0' && value[0] <= '9')) {
		return false
	}
	for _, ch := range value {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '_' && ch != '.' && ch != '-' {
			return false
		}
	}
	return true
}

// Requirement describes the driver release and target requested by a caller.
type Requirement struct {
	DriverID          string
	VersionConstraint string
	Target            Target
}

// SourceSpec identifies a source and its source-specific reference. Reference
// is interpreted by the resolver for Type; it may be a registry URL, project
// identity, or local path.
type SourceSpec struct {
	Type      string
	Reference string
}

// ResolvedRelease is a source-independent release snapshot. Artifacts may be a
// partial target set; callers must not infer that omitted targets exist.
type ResolvedRelease struct {
	DriverID  string
	Version   string
	Source    SourceSpec
	Evidence  Evidence
	Artifacts []Artifact
}

// Evidence records the immutable metadata used to resolve a source. It is
// separate from SourceSpec because evidence describes one resolution event,
// while source identity remains stable across resolutions.
type Evidence struct {
	BundleURL       string
	BundleHash      string
	ReleaseListURL  string
	ReleaseListHash string
}

// Artifact describes one downloadable archive for one concrete target. Hash and
// Size are optional so older registry entries remain representable.
type Artifact struct {
	Target           Target
	Format           string
	URL              string
	Hash             string
	Size             *int64
	HostRequirements HostRequirements
}

// HostRequirements contains runtime requirements declared by a release.
type HostRequirements struct {
	OSMin    string
	GLibCMin string
	Libs     []string
	Bins     []NamedRequirement
}

// NamedRequirement is a host library or binary requirement and its minimum
// version, when one is specified.
type NamedRequirement struct {
	Name string
	Min  string
}

// ValidateArtifactMetadata validates the optional archive digest and size.
// The current metadata contract supports SHA-256 only and uses the canonical
// "sha256:<lowercase hex>" representation.
func ValidateArtifactMetadata(hash string, size *int64) error {
	if hash != "" {
		algorithm, digest, found := strings.Cut(hash, ":")
		if !found {
			return fmt.Errorf("artifact hash must include an algorithm prefix")
		}
		if algorithm != "sha256" {
			return fmt.Errorf("unsupported artifact hash algorithm %q", algorithm)
		}
		if len(digest) != 64 {
			return fmt.Errorf("sha256 artifact hash must contain 64 lowercase hexadecimal characters")
		}
		for _, ch := range digest {
			if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
				return fmt.Errorf("sha256 artifact hash must contain 64 lowercase hexadecimal characters")
			}
		}
	}
	if size != nil && *size < 0 {
		return fmt.Errorf("artifact size must not be negative")
	}
	return nil
}

// ValidateResolvedRelease checks the contract required before a release can
// become a lockfile snapshot. Registry candidates may omit hashes or sizes;
// only the snapshot boundary requires them to be finalized.
func ValidateResolvedRelease(release ResolvedRelease) error {
	if len(release.Artifacts) == 0 {
		return fmt.Errorf("resolved release must contain at least one artifact")
	}

	seenTargets := make(map[Target]struct{}, len(release.Artifacts))
	seenLocations := make(map[string]Artifact, len(release.Artifacts))
	for i, artifact := range release.Artifacts {
		if artifact.URL == "" {
			return fmt.Errorf("artifact %d has no resolved URL", i)
		}
		if canonical := CanonicalTarget(artifact.Target); canonical != artifact.Target {
			return fmt.Errorf("artifact %d target is not canonical", i)
		}
		if err := ValidateConcreteTarget(artifact.Target); err != nil {
			return fmt.Errorf("artifact %d has invalid target: %w", i, err)
		}
		if _, ok := seenTargets[artifact.Target]; ok {
			return fmt.Errorf("resolved release contains duplicate target")
		}
		seenTargets[artifact.Target] = struct{}{}
		if prior, ok := seenLocations[artifact.URL]; ok {
			if prior.Hash != artifact.Hash || !sameSize(prior.Size, artifact.Size) {
				return fmt.Errorf("artifacts sharing a location have conflicting hash or size")
			}
		} else {
			seenLocations[artifact.URL] = artifact
		}

		if artifact.Hash == "" {
			return fmt.Errorf("artifact %d has no finalized hash", i)
		}
		if artifact.Size == nil {
			return fmt.Errorf("artifact %d has no finalized size", i)
		}
		if err := ValidateArtifactMetadata(artifact.Hash, artifact.Size); err != nil {
			return fmt.Errorf("artifact %d has invalid metadata: %w", i, err)
		}
	}
	return nil
}

func sameSize(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
