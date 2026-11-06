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

// Package resolution contains module-internal target and archive metadata.
package resolution

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var sha256RE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Target identifies the platform an artifact runs on.
type Target struct {
	OS      string `toml:"os" json:"os"`
	Arch    string `toml:"arch" json:"arch"`
	LibC    string `toml:"libc,omitempty" json:"libc,omitempty"`
	Variant string `toml:"variant,omitempty" json:"variant,omitempty"`
}

// TargetFromPlatformTuple adapts a legacy platform tuple to the structured
// target schema. Only known libc tokens are interpreted; callers needing
// a tuple for a registry URL must retain the original spelling separately.
func TargetFromPlatformTuple(tuple string) (Target, error) {
	parts := strings.Split(tuple, "_")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return Target{}, fmt.Errorf("invalid platform tuple %q", tuple)
	}
	t := Target{OS: parts[0], Arch: parts[1]}
	idx := 2
	if parts[1] == "x86" && len(parts) > 2 && parts[2] == "64" {
		t.Arch = "x86_64"
		idx = 3
	}
	for _, part := range parts[idx:] {
		if part == "" {
			return Target{}, fmt.Errorf("platform tuple %q contains an empty component", tuple)
		}
		switch part {
		case "gnu", "musl":
			if t.LibC != "" {
				return Target{}, fmt.Errorf("platform tuple %q contains multiple libc components", tuple)
			}
			t.LibC = part
		default:
			if t.Variant != "" {
				t.Variant += "_"
			}
			t.Variant += part
		}
	}
	t = CanonicalTarget(t)
	if err := ValidateConcreteTarget(t); err != nil {
		return Target{}, fmt.Errorf("invalid platform tuple %q: %w", tuple, err)
	}
	return t, nil
}

// CanonicalTarget applies established aliases at adapter boundaries. Unknown
// tokens are preserved rather than guessed.
func CanonicalTarget(t Target) Target {
	if t.OS == "darwin" {
		t.OS = "macos"
	}
	switch t.Arch {
	case "x86_64":
		t.Arch = "amd64"
	case "aarch64":
		t.Arch = "arm64"
	}
	if t.OS == "linux" && t.LibC == "" {
		t.LibC = "gnu"
	}
	return t
}

// ValidateConcreteTarget validates the concrete token syntax without
// restricting targets to a predefined platform inventory.
func ValidateConcreteTarget(t Target) error {
	if t.OS == "" || t.Arch == "" {
		return fmt.Errorf("target OS and architecture are required")
	}
	for _, token := range []struct{ name, value string }{
		{"OS", t.OS}, {"architecture", t.Arch}, {"libc", t.LibC}, {"variant", t.Variant},
	} {
		if token.value != "" && !validTargetToken(token.value) {
			return fmt.Errorf("target %s %q is invalid", token.name, token.value)
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

type ArtifactLocationKind string

const (
	ArtifactLocationURL          ArtifactLocationKind = "url"
	ArtifactLocationPath         ArtifactLocationKind = "path"
	ArtifactLocationRegistryPath ArtifactLocationKind = "registry-path"
)

// ArtifactLocation identifies where bytes are located.
type ArtifactLocation struct {
	Kind  ArtifactLocationKind `toml:"kind"`
	Value string               `toml:"value"`
}

// ValidateLocalPath checks the filesystem-path contract without resolving or
// opening anything. Windows drive paths remain valid on every reader host.
func ValidateLocalPath(value string) error {
	if value == "" || strings.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("artifact path must be nonempty and must not contain NUL")
	}
	if len(value) >= 3 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) &&
		value[1] == ':' && (value[2] == '/' || value[2] == '\\') {
		return nil
	}
	parsed, err := url.Parse(value)
	if strings.Contains(value, "://") || (err == nil && parsed.Scheme != "") {
		return fmt.Errorf("artifact path must not use URI syntax")
	}
	return nil
}

// Artifact is a distributable archive for one target.
type Artifact struct {
	Target           Target           `toml:"target"`
	Format           string           `toml:"format"`
	Location         ArtifactLocation `toml:"location"`
	Hash             string           `toml:"hash"`
	Size             *int64           `toml:"size,omitempty"`
	HostRequirements HostRequirements `toml:"host_requirements,omitempty"`
}

type EvidenceKind string

const (
	EvidenceKindReleaseMetadata EvidenceKind = "release-metadata"
	EvidenceKindReleaseIndex    EvidenceKind = "release-index"
)

// Evidence records release metadata used to resolve an artifact.
type Evidence struct {
	Kind     EvidenceKind     `toml:"kind"`
	Location ArtifactLocation `toml:"location"`
	Hash     string           `toml:"hash"`
}

// HostRequirements describes platform requirements needed by an artifact.
type HostRequirements struct {
	OSMin    string             `toml:"os_min,omitempty"`
	GLibCMin string             `toml:"glibc_min,omitempty"`
	Libs     []string           `toml:"libs,omitempty"`
	Bins     []NamedRequirement `toml:"bins,omitempty"`
}

// NamedRequirement identifies a required executable and its minimum version.
type NamedRequirement struct {
	Name string `toml:"name"`
	Min  string `toml:"min,omitempty"`
}

// ValidateEvidence rejects unknown kinds, malformed hashes, duplicate
// kind/location pairs, and conflicting hashes at the same location.
func ValidateEvidence(evidence []Evidence) error {
	type evidenceKey struct {
		evidenceKind EvidenceKind
		location     ArtifactLocation
	}
	seen := make(map[evidenceKey]bool)
	hashes := make(map[ArtifactLocation]string)
	for _, e := range evidence {
		if e.Kind != EvidenceKindReleaseMetadata && e.Kind != EvidenceKindReleaseIndex {
			return fmt.Errorf("unknown evidence kind %q", e.Kind)
		}
		if e.Location.Kind != ArtifactLocationURL && e.Location.Kind != ArtifactLocationPath && e.Location.Kind != ArtifactLocationRegistryPath {
			return fmt.Errorf("unknown evidence location kind %q", e.Location.Kind)
		}
		if e.Location.Value == "" {
			return fmt.Errorf("evidence location is empty")
		}
		if !sha256RE.MatchString(e.Hash) {
			return fmt.Errorf("evidence has malformed SHA-256 hash")
		}
		loc := e.Location
		key := evidenceKey{e.Kind, loc}
		if seen[key] {
			return fmt.Errorf("duplicate evidence kind %q at %s", e.Kind, e.Location.Value)
		}
		seen[key] = true
		if prev, ok := hashes[loc]; ok && prev != e.Hash {
			return fmt.Errorf("evidence location %s appears with conflicting hashes", e.Location.Value)
		}
		hashes[loc] = e.Hash
	}
	return nil
}
