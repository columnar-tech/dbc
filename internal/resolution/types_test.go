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

package resolution

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateResolvedRelease(t *testing.T) {
	size := int64(12)
	complete := ResolvedRelease{
		DriverID: "example-driver",
		Version:  "1.2.3",
		Source: SourceSpec{
			Type:      "packslip",
			Reference: "github.com/example/example-driver",
		},
		Evidence: []Evidence{{
			Kind:     EvidenceKindReleaseMetadata,
			Location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "https://example.test/release.sigstore.json"},
			Hash:     "sha256:" + strings.Repeat("a", 64),
		}},
		Artifacts: []Artifact{{
			Target:   Target{OS: "linux", Arch: "amd64", LibC: "gnu"},
			Format:   "tar.gz",
			Location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "https://example.test/driver.tar.gz"},
			Hash:     "sha256:" + strings.Repeat("b", 64),
			Size:     &size,
			HostRequirements: HostRequirements{
				OSMin:    "1.0",
				GLibCMin: "2.17",
				Libs:     []string{"libc.so.6"},
				Bins:     []NamedRequirement{{Name: "java", Min: "17"}},
			},
		}},
	}

	require.NoError(t, ValidateResolvedRelease(complete))
	assert.Equal(t, "packslip", complete.Source.Type)
	assert.Equal(t, "https://example.test/release.sigstore.json", complete.Evidence[0].Location.Value)
	assert.Equal(t, "2.17", complete.Artifacts[0].HostRequirements.GLibCMin)
}

func TestValidateEvidence(t *testing.T) {
	metadata := Evidence{
		Kind:     EvidenceKindReleaseMetadata,
		Location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "https://example.test/metadata"},
		Hash:     "sha256:" + strings.Repeat("a", 64),
	}
	index := Evidence{
		Kind:     EvidenceKindReleaseIndex,
		Location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "https://example.test/index"},
		Hash:     "sha256:" + strings.Repeat("b", 64),
	}
	pathEvidence := Evidence{
		Kind:     EvidenceKindReleaseIndex,
		Location: ArtifactLocation{Kind: ArtifactLocationPath, Value: "./cache/release-index.json"},
		Hash:     "sha256:" + strings.Repeat("c", 64),
	}
	assert.NoError(t, ValidateEvidence(nil), "sources without separate evidence may use an empty slice")
	assert.NoError(t, ValidateEvidence([]Evidence{metadata, index}))
	assert.NoError(t, ValidateEvidence([]Evidence{pathEvidence}), "evidence locations may be local paths")

	tests := []struct {
		name     string
		evidence []Evidence
		wantErr  string
	}{
		{name: "empty kind", evidence: []Evidence{{Location: metadata.Location, Hash: metadata.Hash}}, wantErr: "unsupported kind"},
		{name: "unknown kind", evidence: []Evidence{{Kind: "signature", Location: metadata.Location, Hash: metadata.Hash}}, wantErr: "unsupported kind"},
		{name: "empty location kind", evidence: []Evidence{{Kind: metadata.Kind, Location: ArtifactLocation{Value: metadata.Location.Value}, Hash: metadata.Hash}}, wantErr: "invalid location"},
		{name: "empty location value", evidence: []Evidence{{Kind: metadata.Kind, Location: ArtifactLocation{Kind: ArtifactLocationURL}, Hash: metadata.Hash}}, wantErr: "invalid location"},
		{name: "invalid location", evidence: []Evidence{{Kind: metadata.Kind, Location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "file:///tmp/evidence"}, Hash: metadata.Hash}}, wantErr: "invalid location"},
		{name: "missing hash", evidence: []Evidence{{Kind: metadata.Kind, Location: metadata.Location}}, wantErr: "no hash"},
		{name: "invalid hash", evidence: []Evidence{{Kind: metadata.Kind, Location: metadata.Location, Hash: "sha512:abcd"}}, wantErr: "invalid hash"},
		{name: "duplicate exact", evidence: []Evidence{metadata, metadata}, wantErr: "duplicate kind and location"},
		{name: "duplicate conflicting hash", evidence: []Evidence{metadata, {Kind: metadata.Kind, Location: metadata.Location, Hash: index.Hash}}, wantErr: "duplicate kind and location"},
		{name: "same location conflicting across kinds", evidence: []Evidence{metadata, {Kind: index.Kind, Location: metadata.Location, Hash: index.Hash}}, wantErr: "conflicting hashes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.ErrorContains(t, ValidateEvidence(test.evidence), test.wantErr)
		})
	}
	assert.NoError(t, ValidateEvidence([]Evidence{metadata, {
		Kind: EvidenceKindReleaseIndex, Location: metadata.Location, Hash: metadata.Hash,
	}}), "different kinds may share a typed location when their hashes agree")
}

func TestValidateResolvedReleaseRequiresFinalizedArtifacts(t *testing.T) {
	size := int64(12)
	base := ResolvedRelease{Artifacts: []Artifact{{
		Target:   Target{OS: "linux", Arch: "amd64", LibC: "gnu"},
		Location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "https://example.test/driver.tar.gz"},
		Hash:     "sha256:" + strings.Repeat("a", 64),
		Size:     &size,
	}}}

	tests := []struct {
		name    string
		mutate  func(*ResolvedRelease)
		wantErr string
	}{
		{name: "missing hash", mutate: func(r *ResolvedRelease) { r.Artifacts[0].Hash = "" }, wantErr: "no finalized hash"},
		{name: "missing size", mutate: func(r *ResolvedRelease) { r.Artifacts[0].Size = nil }, wantErr: "no finalized size"},
		{name: "missing location", mutate: func(r *ResolvedRelease) { r.Artifacts[0].Location = ArtifactLocation{} }, wantErr: "invalid location"},
		{name: "duplicate target", mutate: func(r *ResolvedRelease) {
			duplicate := r.Artifacts[0]
			r.Artifacts = append(r.Artifacts, duplicate)
		}, wantErr: "duplicate target"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := base
			release.Artifacts = append([]Artifact(nil), base.Artifacts...)
			tt.mutate(&release)
			require.ErrorContains(t, ValidateResolvedRelease(release), tt.wantErr)
		})
	}
}

func TestValidateResolvedReleaseKeepsRegistryCandidateWithoutHashValid(t *testing.T) {
	size := int64(12)
	candidate := ResolvedRelease{Artifacts: []Artifact{{
		Target:   Target{OS: "linux", Arch: "amd64", LibC: "gnu"},
		Location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "https://registry.example.test/driver.tar.gz"},
		Size:     &size,
	}}}

	assert.ErrorContains(t, ValidateResolvedRelease(candidate), "no finalized hash")
	assert.NoError(t, ValidateArtifactMetadata("", nil))
}

func TestTargetAliasesAndTupleAdapter(t *testing.T) {
	tests := []struct {
		platform string
		want     Target
	}{
		{platform: "darwin_aarch64", want: Target{OS: "macos", Arch: "arm64"}},
		{platform: "linux_x86_64", want: Target{OS: "linux", Arch: "amd64", LibC: "gnu"}},
		{platform: "linux_amd64_musl_v3", want: Target{OS: "linux", Arch: "amd64", LibC: "musl", Variant: "v3"}},
		{platform: "plan9_oddarch", want: Target{OS: "plan9", Arch: "oddarch"}},
	}
	for _, test := range tests {
		t.Run(test.platform, func(t *testing.T) {
			got, err := TargetFromPlatformTuple(test.platform)
			assert.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestEmptyConcreteTargetIsNotAWildcard(t *testing.T) {
	assert.Error(t, ValidateConcreteTarget(Target{}))
	assert.NoError(t, ValidateConcreteTarget(Target{OS: "linux", Arch: "amd64", LibC: "gnu"}),
		"empty variant identifies the ordinary concrete variant")
}

func TestResolvedReleaseAllowsSharedLocationButRejectsConflictingMetadata(t *testing.T) {
	size := int64(12)
	base := Artifact{
		Target: Target{OS: "linux", Arch: "amd64", LibC: "gnu"}, Location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "https://example.test/shared.tar.gz"},
		Hash: "sha256:" + strings.Repeat("a", 64), Size: &size,
	}
	release := ResolvedRelease{Artifacts: []Artifact{base, {
		Target: Target{OS: "macos", Arch: "arm64"}, Location: base.Location, Hash: base.Hash, Size: &size,
	}}}
	assert.NoError(t, ValidateResolvedRelease(release))
	release.Artifacts[1].Hash = "sha256:" + strings.Repeat("b", 64)
	assert.ErrorContains(t, ValidateResolvedRelease(release), "conflicting hash or size")
}

func TestValidateArtifactLocation(t *testing.T) {
	tests := []struct {
		name     string
		location ArtifactLocation
		wantErr  string
	}{
		{name: "http URL", location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "http://example.test/a%2Fb?token=x%2Fy"}},
		{name: "HTTPS URL spelling preserved", location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "HTTPS://Example.test/a%2Fb?token=x%2Fy"}},
		{name: "relative path", location: ArtifactLocation{Kind: ArtifactLocationPath, Value: "./packages/archive.tar.gz"}},
		{name: "absolute path", location: ArtifactLocation{Kind: ArtifactLocationPath, Value: "/var/cache/archive.tar.gz"}},
		{name: "Windows path", location: ArtifactLocation{Kind: ArtifactLocationPath, Value: `C:\cache\archive.tar.gz`}},
		{name: "empty kind", location: ArtifactLocation{Value: "https://example.test/a"}, wantErr: "unsupported artifact location kind"},
		{name: "unknown kind", location: ArtifactLocation{Kind: "other", Value: "anything"}, wantErr: "unsupported artifact location kind"},
		{name: "empty value", location: ArtifactLocation{Kind: ArtifactLocationPath}, wantErr: "value is empty"},
		{name: "relative URL", location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "/archive.tar.gz"}, wantErr: "absolute HTTP(S)"},
		{name: "file URL", location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "file:///tmp/archive.tar.gz"}, wantErr: "absolute HTTP(S)"},
		{name: "opaque URL", location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "https:archive.tar.gz"}, wantErr: "absolute HTTP(S)"},
		{name: "userinfo URL", location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "https://user:pass@example.test/archive"}, wantErr: "without userinfo"},
		{name: "fragment URL", location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "https://example.test/archive#section"}, wantErr: "without userinfo"},
		{name: "empty fragment URL", location: ArtifactLocation{Kind: ArtifactLocationURL, Value: "https://example.test/archive#"}, wantErr: "without userinfo"},
		{name: "path NUL", location: ArtifactLocation{Kind: ArtifactLocationPath, Value: "archive\x00.tar.gz"}, wantErr: "NUL"},
		{name: "path URL", location: ArtifactLocation{Kind: ArtifactLocationPath, Value: "https://example.test/archive"}, wantErr: "URI syntax"},
		{name: "path file URI", location: ArtifactLocation{Kind: ArtifactLocationPath, Value: "file:///tmp/archive"}, wantErr: "URI syntax"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateArtifactLocation(test.location)
			if test.wantErr != "" {
				assert.ErrorContains(t, err, test.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestResolvedReleaseLocationIdentityIncludesKind(t *testing.T) {
	urlLocation := ArtifactLocation{Kind: ArtifactLocationURL, Value: "https://example.test/archive"}
	pathLocation := ArtifactLocation{Kind: ArtifactLocationPath, Value: urlLocation.Value}
	assert.NotEqual(t, urlLocation, pathLocation, "location identity includes its kind")
}
