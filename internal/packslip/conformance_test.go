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

package packslip

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

type conformanceCases struct {
	Cases []json.RawMessage `json:"cases"`
}

func loadConformanceCases(t *testing.T, name string) []json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("testdata/conformance/" + name)
	require.NoError(t, err)
	var fixture conformanceCases
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.NotEmpty(t, fixture.Cases, "%s must contain cases", name)
	return fixture.Cases
}

func decodeConformanceCase[T any](t *testing.T, raw json.RawMessage, index int) (string, T) {
	t.Helper()
	var item struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(raw, &item), "case %d must be an object", index)
	require.NotEmpty(t, item.Name, "case %d must have a name", index)
	var value T
	require.NoError(t, json.Unmarshal(raw, &value), "%s", item.Name)
	return item.Name, value
}

func TestPackslipArtifactSelectionConformance(t *testing.T) {
	type host struct {
		OS   string          `json:"os"`
		Arch string          `json:"arch"`
		LibC json.RawMessage `json:"libc"`
	}
	type expectation struct {
		Artifact *string `json:"artifact"`
		Error    *string `json:"error"`
	}
	type testCase struct {
		Host      *host             `json:"host"`
		Variant   json.RawMessage   `json:"variant"`
		Formats   []string          `json:"formats"`
		Artifacts []releaseArtifact `json:"artifacts"`
		Expect    expectation       `json:"expect"`
	}

	for index, raw := range loadConformanceCases(t, "artifact-selection.json") {
		name, test := decodeConformanceCase[testCase](t, raw, index)
		t.Run(name, func(t *testing.T) {
			require.NotNil(t, test.Host, "host is required")
			require.NotEmpty(t, test.Host.OS, "host.os is required")
			require.NotEmpty(t, test.Host.Arch, "host.arch is required")
			require.NotEmpty(t, test.Host.LibC, "host.libc is required (use null when unknown)")
			require.NotEmpty(t, test.Variant, "variant is required (use null for the ordinary variant)")
			require.NotEmpty(t, test.Formats, "formats is required")
			require.NotEmpty(t, test.Artifacts, "artifacts is required")
			require.True(t, test.Expect.Artifact != nil || test.Expect.Error != nil, "expect must specify an artifact or error")
			require.False(t, test.Expect.Artifact != nil && test.Expect.Error != nil, "expect cannot specify both artifact and error")
			target := canonicalizeTargetAliases(Target{OS: test.Host.OS, Arch: test.Host.Arch})
			var libc, variant *string
			require.NoError(t, json.Unmarshal(test.Host.LibC, &libc), "host.libc must be a string or null")
			require.NoError(t, json.Unmarshal(test.Variant, &variant), "variant must be a string or null")
			if libc != nil {
				target.LibC = *libc
			}
			target.Variant = stringValue(variant)
			for i := range test.Artifacts {
				require.NotEmpty(t, test.Artifacts[i].Name, "artifact name is required")
				require.NotNil(t, test.Artifacts[i].Format, "artifact format is required")
				canonicalizeArtifactAliases(&test.Artifacts[i])
			}
			release := &parsedRelease{predicate: releasePredicate{Artifacts: test.Artifacts}}
			selected, err := selectArtifactByFormats(release, target, test.Formats)
			if test.Expect.Artifact != nil {
				require.NoError(t, err)
				require.Equal(t, *test.Expect.Artifact, selected.Name)
				return
			}
			switch *test.Expect.Error {
			case "no-match":
				require.ErrorIs(t, err, ErrReleaseNotFound)
			case "ambiguous":
				require.ErrorIs(t, err, ErrAmbiguousArtifact)
			default:
				t.Fatalf("unknown expected selection error %q", *test.Expect.Error)
			}
		})
	}
}

func TestPackslipStatementValidityConformance(t *testing.T) {
	type testCase struct {
		Expect    string          `json:"expect"`
		Statement json.RawMessage `json:"statement"`
	}
	for index, raw := range loadConformanceCases(t, "statement-validity.json") {
		name, test := decodeConformanceCase[testCase](t, raw, index)
		t.Run(name, func(t *testing.T) {
			require.NotEmpty(t, test.Expect, "expect is required")
			require.NotEmpty(t, test.Statement, "statement is required")
			parsed, err := parseRelease(test.Statement, "")
			switch test.Expect {
			case "accept":
				require.NoError(t, err)
				require.NotNil(t, parsed)
			case "reject":
				require.Error(t, err)
			default:
				t.Fatalf("unknown expected statement outcome %q", test.Expect)
			}
		})
	}
}

func TestPackslipTagVersionConformance(t *testing.T) {
	type testCase struct {
		Tag     string          `json:"tag"`
		Project string          `json:"project"`
		Expect  json.RawMessage `json:"expect"`
	}
	for index, raw := range loadConformanceCases(t, "tag-versions.json") {
		name, test := decodeConformanceCase[testCase](t, raw, index)
		t.Run(name, func(t *testing.T) {
			require.NotEmpty(t, test.Tag, "tag is required")
			require.NotEmpty(t, test.Project, "project is required")
			require.NotEmpty(t, test.Expect, "expect is required")
			version, ok := tagVersion(test.Tag, test.Project)
			if string(test.Expect) == "null" {
				require.False(t, ok)
				return
			}
			var expected string
			require.NoError(t, json.Unmarshal(test.Expect, &expected))
			require.True(t, ok, "expected version %s", expected)
			require.Equal(t, expected, version)
		})
	}
}
