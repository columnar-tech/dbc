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
		Evidence: Evidence{
			BundleURL:  "https://example.test/release.sigstore.json",
			BundleHash: "sha256:" + strings.Repeat("a", 64),
		},
		Artifacts: []Artifact{{
			Platform: "linux_amd64",
			OS:       "linux",
			Arch:     "amd64",
			LibC:     "gnu",
			Format:   "tar.gz",
			URL:      "https://example.test/driver.tar.gz",
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
	assert.Equal(t, "https://example.test/release.sigstore.json", complete.Evidence.BundleURL)
	assert.Equal(t, "2.17", complete.Artifacts[0].HostRequirements.GLibCMin)
}

func TestValidateResolvedReleaseRequiresFinalizedArtifacts(t *testing.T) {
	size := int64(12)
	base := ResolvedRelease{Artifacts: []Artifact{{
		URL:  "https://example.test/driver.tar.gz",
		Hash: "sha256:" + strings.Repeat("a", 64),
		Size: &size,
	}}}

	tests := []struct {
		name    string
		mutate  func(*ResolvedRelease)
		wantErr string
	}{
		{name: "missing hash", mutate: func(r *ResolvedRelease) { r.Artifacts[0].Hash = "" }, wantErr: "no finalized hash"},
		{name: "missing size", mutate: func(r *ResolvedRelease) { r.Artifacts[0].Size = nil }, wantErr: "no finalized size"},
		{name: "missing URL", mutate: func(r *ResolvedRelease) { r.Artifacts[0].URL = "" }, wantErr: "no resolved URL"},
		{name: "duplicate artifact URL", mutate: func(r *ResolvedRelease) {
			duplicate := r.Artifacts[0]
			duplicate.Platform = "macos_arm64"
			r.Artifacts = append(r.Artifacts, duplicate)
		}, wantErr: "duplicate artifact identity"},
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
		URL:  "https://registry.example.test/driver.tar.gz",
		Size: &size,
	}}}

	assert.ErrorContains(t, ValidateResolvedRelease(candidate), "no finalized hash")
	assert.NoError(t, ValidateArtifactMetadata("", nil))
}
