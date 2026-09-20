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

// Requirement describes the driver release and target requested by a caller.
type Requirement struct {
	DriverID          string
	VersionConstraint string
	Platform          string
}

// SourceSpec identifies a source and its source-specific reference. Reference
// is interpreted by the resolver for Type; it may be a registry URL, project
// identity, or local path.
type SourceSpec struct {
	Type      string
	Reference string
}

// ResolvedRelease is a source-independent release snapshot. Artifacts may be a
// partial platform set; callers must not infer that omitted platforms exist.
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

// Artifact describes one downloadable archive. Hash and Size are optional so
// older registry entries and locally resolved partial sets remain representable.
type Artifact struct {
	// Platform retains the registry tuple during migration to explicit selectors.
	Platform         string
	OS               string
	Arch             string
	LibC             string
	Variant          string
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

	// Source models do not yet assign stable artifact IDs, so the resolved URL
	// is the identity used to reject duplicate records within one release.
	seen := make(map[string]struct{}, len(release.Artifacts))
	for i, artifact := range release.Artifacts {
		if artifact.URL == "" {
			return fmt.Errorf("artifact %d has no resolved URL", i)
		}
		if _, ok := seen[artifact.URL]; ok {
			return fmt.Errorf("resolved release contains duplicate artifact identity")
		}
		seen[artifact.URL] = struct{}{}

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
