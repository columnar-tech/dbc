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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testProject = "github.com/acme/driver"

func ptr[T any](value T) *T { return &value }

func validRelease(version string, artifacts ...releaseArtifact) []byte {
	if len(artifacts) == 0 {
		format := "tar.gz"
		artifacts = []releaseArtifact{{Name: "driver-linux.tar.gz", OS: ptr("linux"), Arch: ptr("x86_64"), LibC: ptr("gnu"), Size: ptr(uint64(10)), Format: &format, URL: ptr("https://dl.example/driver-linux.tar.gz")}}
	}
	for i := range artifacts {
		if artifacts[i].Extensions == nil {
			artifacts[i].Extensions = map[string]json.RawMessage{"dbc": mustJSON(dbcArtifactExtension{PackageVersion: 2})}
		}
	}
	project := testProject
	issued := GitHubOIDCIssuer
	envelope := statementEnvelope{
		Type:          StatementType,
		PredicateType: ReleasePredicateType,
		Predicate: mustJSON(releasePredicate{
			Project: project, Version: version, PublishedAt: "2026-09-01T12:00:00Z", Artifacts: artifacts,
			Extensions: map[string]json.RawMessage{"dbc": mustJSON(dbcReleaseExtension{SchemaVersion: 1, DriverID: "driver"})},
			Identity:   releaseIdentity{Scheme: "sigstore-oidc", KeyID: "https://github.com/acme/driver/.github/workflows/release.yml@refs/tags/v1", Issuer: &issued},
		}),
	}
	for _, artifact := range artifacts {
		envelope.Subject = append(envelope.Subject, subject{Name: artifact.Name, Digest: map[string]string{"sha256": strings.Repeat("a", 64)}})
	}
	return mustJSON(envelope)
}

func parseDBCRelease(payload []byte, expectedProject string) (*parsedRelease, error) {
	release, err := parseRelease(payload, expectedProject)
	if err != nil {
		return nil, err
	}
	if err := validateDBCReleaseExtensions(release); err != nil {
		return nil, err
	}
	return release, nil
}

func validList(sequence uint64, expires string) []byte {
	issued := GitHubOIDCIssuer
	url := "https://github.com/acme/driver/releases/download/v1/packslip.sigstore.json"
	envelope := statementEnvelope{
		Type:          StatementType,
		PredicateType: ListPredicateType,
		Subject:       []subject{{Name: url, Digest: map[string]string{"sha256": strings.Repeat("b", 64)}}},
		Predicate: mustJSON(releaseListPredicate{
			Project: testProject, Generated: "2026-09-01T12:00:00Z", Expires: expires, Sequence: &sequence,
			Identity: releaseIdentity{Scheme: "sigstore-oidc", KeyID: "https://github.com/acme/driver/.github/workflows/release.yml@refs/heads/main", Issuer: &issued},
			Releases: []releaseRef{{Version: "1.0.0", Tag: ptr("v1.0.0"), PublishedAt: "2026-09-01T12:00:00Z", Packslip: url}},
		}),
	}
	return mustJSON(envelope)
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func TestParsePackslipReleaseStrictlyValidatesProjectAndSubjects(t *testing.T) {
	parsed, err := parseRelease(validRelease("1.2.3"), testProject)
	require.NoError(t, err)
	require.Equal(t, "1.2.3", parsed.predicate.Version)
	require.Equal(t, strings.Repeat("a", 64), parsed.byName["driver-linux.tar.gz"].Digest["sha256"])

	tests := []struct {
		name    string
		payload []byte
		project string
		wantErr string
	}{
		{name: "wrong project", payload: validRelease("1.2.3"), project: "github.com/attacker/driver", wantErr: "does not match requested project"},
		{name: "bad semver", payload: validRelease("01.2.3"), project: testProject, wantErr: "not SemVer"},
	}
	for _, test := range tests[:2] {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseRelease(test.payload, test.project)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
	mixedCase := strings.Replace(string(validRelease("1.2.3")), `"project":"github.com/acme/driver"`, `"project":"GitHub.COM/ACME/DRIVER"`, 1)
	canonical, err := parseRelease([]byte(mixedCase), testProject)
	require.NoError(t, err)
	require.Equal(t, testProject, canonical.predicate.Project)
	missingSubject := validRelease("1.2.3")
	var missingEnvelope statementEnvelope
	require.NoError(t, json.Unmarshal(missingSubject, &missingEnvelope))
	missingEnvelope.Subject = nil
	_, err = parseRelease(mustJSON(missingEnvelope), testProject)
	require.ErrorContains(t, err, "has no subject")

	badDigest := validRelease("1.2.3")
	var envelope statementEnvelope
	require.NoError(t, json.Unmarshal(badDigest, &envelope))
	envelope.Subject[0].Digest["sha256"] = "UPPER" + strings.Repeat("a", 58)
	badDigest = mustJSON(envelope)
	_, err = parseRelease(badDigest, testProject)
	require.ErrorContains(t, err, "lowercase SHA-256")

	unknownField := strings.Replace(string(validRelease("1.2.3")), `"version":"1.2.3"`, `"version":"1.2.3","unexpected":true`, 1)
	_, err = parseRelease([]byte(unknownField), testProject)
	require.NoError(t, err, "unknown optional wire fields are ignored")

	duplicateField := strings.Replace(string(validRelease("1.2.3")), `"_type":"`+StatementType+`"`, `"_type":"`+StatementType+`","_type":"`+StatementType+`"`, 1)
	_, err = parseRelease([]byte(duplicateField), testProject)
	require.ErrorContains(t, err, "duplicate JSON field")
}

func TestParseSignedReleaseListRejectsMalformedAndExpiredShape(t *testing.T) {
	list, err := parseList(validList(4, "2026-10-01T00:00:00Z"), testProject)
	require.NoError(t, err)
	require.Equal(t, uint64(4), *list.predicate.Sequence)

	_, err = parseList(validList(4, "2026-09-01T12:00:00Z"), testProject)
	require.ErrorContains(t, err, "expires_at must be after generated_at")
	_, err = parseList(validList(4, "2026-10-01T00:00:00Z"), "github.com/other/repo")
	require.ErrorContains(t, err, "does not match requested project")

	var envelope statementEnvelope
	require.NoError(t, json.Unmarshal(validList(4, "2026-10-01T00:00:00Z"), &envelope))
	envelope.Subject[0].Digest["sha256"] = strings.Repeat("0", 64)
	_, err = parseList(mustJSON(envelope), testProject)
	require.NoError(t, err) // The list's own subject is valid; resolver compares fetched bundle bytes.
}

func TestSelectArtifactUsesPackslipOrderAndRejectsAmbiguity(t *testing.T) {
	makeArtifact := func(name, osName, arch, libc, format string, variant *string) releaseArtifact {
		return releaseArtifact{Name: name, OS: optionalToken(osName), Arch: optionalToken(arch), LibC: optionalToken(libc), Variant: variant, Size: ptr(uint64(1)), Format: ptr(format), URL: ptr("https://dl.example/" + name)}
	}
	release, err := parseDBCRelease(validRelease("1.0.0",
		makeArtifact("portable.tar.gz", "", "", "", "tar.gz", nil),
		makeArtifact("linux-specific.tgz", "linux", "x86_64", "gnu", "tgz", nil),
		makeArtifact("linux-specific.tar.gz", "linux", "x86_64", "gnu", "tar.gz", nil),
	), testProject)
	require.NoError(t, err)
	require.NoError(t, validateSupportedArtifactSet(release), "tar.gz and tgz with one selector are resolved by format preference")
	selected, err := selectArtifact(release, Target{OS: "linux", Arch: "x86_64", LibC: "gnu"})
	require.NoError(t, err)
	require.Equal(t, "linux-specific.tar.gz", selected.Name)

	ambiguous, err := parseDBCRelease(validRelease("1.0.0",
		makeArtifact("linux-x64.tar.gz", "linux", "", "", "tar.gz", nil),
		makeArtifact("x64-linux.tar.gz", "", "x86_64", "", "tar.gz", nil),
	), testProject)
	require.NoError(t, err)
	_, err = selectArtifact(ambiguous, Target{OS: "linux", Arch: "x86_64"})
	require.ErrorIs(t, err, ErrAmbiguousArtifact)

	variant := "fips"
	withVariant, err := parseDBCRelease(validRelease("1.0.0",
		makeArtifact("default.tar.gz", "linux", "x86_64", "gnu", "tar.gz", nil),
		makeArtifact("fips.tar.gz", "linux", "x86_64", "gnu", "tar.gz", &variant),
	), testProject)
	require.NoError(t, err)
	selected, err = selectArtifact(withVariant, Target{OS: "linux", Arch: "x86_64", LibC: "gnu", Variant: "fips"})
	require.NoError(t, err)
	require.Equal(t, "fips.tar.gz", selected.Name)
	selected, err = selectArtifact(withVariant, Target{OS: "linux", Arch: "x86_64", LibC: "gnu"})
	require.NoError(t, err)
	require.Equal(t, "default.tar.gz", selected.Name, "an empty target variant selects the ordinary variant")
	_, err = selectArtifact(withVariant, Target{OS: "linux", Arch: "x86_64", LibC: "gnu", Variant: "other"})
	require.ErrorIs(t, err, ErrReleaseNotFound, "variant selectors must match exactly")
}

func TestDBCSupportedFormatsAndPackslipHostLibcSemantics(t *testing.T) {
	makeArtifact := func(name, osName, arch, libc, format string) releaseArtifact {
		return releaseArtifact{Name: name, OS: optionalToken(osName), Arch: optionalToken(arch), LibC: optionalToken(libc), Size: ptr(uint64(1)), Format: ptr(format), URL: ptr("https://dl.example/" + name)}
	}
	release, err := parseDBCRelease(validRelease("1.0.0",
		makeArtifact("linux.tar.xz", "linux", "x86_64", "gnu", "tar.xz"),
		makeArtifact("portable.tar.gz", "", "", "", "tar.gz"),
	), testProject)
	require.NoError(t, err)
	selected, err := selectArtifact(release, Target{OS: "linux", Arch: "x86_64", LibC: "gnu"})
	require.NoError(t, err)
	require.Equal(t, "portable.tar.gz", selected.Name, "dbc must ignore unsupported formats even when they are more specific")

	gnuOnly, err := parseDBCRelease(validRelease("1.0.0", makeArtifact("linux-gnu.tar.gz", "linux", "amd64", "gnu", "tar.gz")), testProject)
	require.NoError(t, err)
	_, err = selectArtifact(gnuOnly, Target{OS: "linux", Arch: "x86_64"})
	require.ErrorIs(t, err, ErrReleaseNotFound, "a host with unknown libc must not be treated as GNU")
}

func TestParseReleaseRejectsExactSelectorDuplicatesButDefersOverlappingScopeTies(t *testing.T) {
	artifact := func(name, osName, arch string) releaseArtifact {
		return releaseArtifact{Name: name, OS: optionalToken(osName), Arch: optionalToken(arch), Size: ptr(uint64(1)), Format: ptr("tar.gz"), URL: ptr("https://dl.example/" + name)}
	}
	_, err := parseDBCRelease(validRelease("1.0.0",
		artifact("first.tar.gz", "linux", "x86_64"),
		artifact("second.tar.gz", "linux", "amd64"),
	), testProject)
	require.ErrorContains(t, err, "duplicate selectors", "generic selector validity also applies to dbc extensions")

	_, err = parseDBCRelease(validRelease("1.0.0",
		artifact("darwin.tar.gz", "darwin", "x86_64"),
		artifact("macos.tar.gz", "macos", "amd64"),
	), testProject)
	require.ErrorContains(t, err, "duplicate selectors", "OS and architecture aliases are canonicalized before duplicate detection")

	release, err := parseDBCRelease(validRelease("1.0.0",
		artifact("linux.tar.gz", "linux", ""),
		artifact("x64.tar.gz", "", "amd64"),
	), testProject)
	require.NoError(t, err, "different overlapping selectors are valid statement data")
	_, err = selectArtifact(release, Target{OS: "linux", Arch: "amd64"})
	require.ErrorIs(t, err, ErrAmbiguousArtifact, "the tie is reported only for a matching host")

	concreteTie, err := parseDBCRelease(validRelease("1.0.0",
		artifact("linux-amd64.tar.gz", "linux", "amd64"),
		releaseArtifact{Name: "linux-gnu.tar.gz", OS: ptr("linux"), LibC: ptr("gnu"), Size: ptr(uint64(1)), Format: ptr("tar.gz"), URL: ptr("https://dl.example/linux-gnu.tar.gz")},
	), testProject)
	require.NoError(t, err)
	targets, err := deriveConcreteTargets(concreteTie)
	require.NoError(t, err)
	require.Equal(t, []Target{{OS: "linux", Arch: "amd64", LibC: "gnu"}}, targets)
	_, err = selectArtifact(concreteTie, targets[0])
	require.ErrorIs(t, err, ErrAmbiguousArtifact, "ambiguity is reported for an actual derived target with tied highest-specificity candidates")
}

func optionalToken(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func TestNormalizeProjectAndTagVersion(t *testing.T) {
	project, err := normalizeProject("GitHub.COM/Acme/driver/Tools/Tool")
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/driver/Tools/Tool", project)
	version, ok := tagVersion("Tool_v1.2.0", project)
	require.True(t, ok)
	require.Equal(t, "1.2.0", version)
	_, ok = tagVersion("tool_v1.2.0", project)
	require.False(t, ok, "monorepo subpaths preserve case")
	_, err = normalizeProject("https://github.com/acme/driver")
	require.Error(t, err)
}

func TestProjectCanonicalizationPreservesMonorepoSubpathCase(t *testing.T) {
	project := "GitHub.COM/ACME/Driver/Arrow/Flight"
	payload := strings.Replace(string(validRelease("1.2.3")), `"project":"github.com/acme/driver"`, `"project":"github.com/acme/driver/Arrow/Flight"`, 1)
	parsed, err := parseRelease([]byte(payload), project)
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/driver/Arrow/Flight", parsed.predicate.Project)

	wrongCase := strings.Replace(payload, `"project":"github.com/acme/driver/Arrow/Flight"`, `"project":"github.com/acme/driver/arrow/Flight"`, 1)
	_, err = parseRelease([]byte(wrongCase), project)
	require.ErrorContains(t, err, "does not match requested project")
}

func TestWireDecoderIgnoresUnknownOptionalFieldsButRejectsMalformedJSON(t *testing.T) {
	payload := string(validRelease("1.2.3"))
	payload = strings.Replace(payload, `"_type":"`+StatementType+`"`, `"_type":"`+StatementType+`","future_statement_field":{"enabled":true}`, 1)
	payload = strings.Replace(payload, `"version":"1.2.3"`, `"version":"1.2.3","future_predicate_field":[1,2,3]`, 1)
	payload = strings.Replace(payload, `"name":"driver-linux.tar.gz"`, `"name":"driver-linux.tar.gz","future_artifact_field":"ignored"`, 1)
	parsed, err := parseRelease([]byte(payload), testProject)
	require.NoError(t, err)
	require.Equal(t, "1.2.3", parsed.predicate.Version)

	_, err = parseRelease(append([]byte(payload), []byte(` {}`)...), testProject)
	require.ErrorContains(t, err, "multiple JSON values")
	badType := strings.Replace(payload, `"version":"1.2.3"`, `"version":true`, 1)
	_, err = parseRelease([]byte(badType), testProject)
	require.Error(t, err)
	duplicate := strings.Replace(payload, `"version":"1.2.3"`, `"version":"1.2.3","version":"1.2.3"`, 1)
	_, err = parseRelease([]byte(duplicate), testProject)
	require.ErrorContains(t, err, "duplicate JSON field")
}

func TestSupportedArtifactInventoryAllowsOverlappingSelectorsAndChoosesMostSpecific(t *testing.T) {
	artifact := func(name, osName, arch string) releaseArtifact {
		return releaseArtifact{Name: name, OS: optionalToken(osName), Arch: optionalToken(arch), Size: ptr(uint64(1)), Format: ptr("tar.gz"), URL: ptr("https://dl.example/" + name)}
	}
	release, err := parseDBCRelease(validRelease("1.0.0",
		artifact("linux-any-arch.tar.gz", "linux", ""),
		artifact("any-os-amd64.tar.gz", "", "amd64"),
		artifact("linux-amd64.tar.gz", "linux", "amd64"),
	), testProject)
	require.NoError(t, err)
	require.NoError(t, validateSupportedArtifactSet(release), "inventory validation does not reject pairwise overlap before concrete target derivation")
	targets, err := deriveConcreteTargets(release)
	require.NoError(t, err)
	require.Equal(t, []Target{{OS: "linux", Arch: "amd64", LibC: "gnu"}}, targets)
	selected, err := selectArtifact(release, targets[0])
	require.NoError(t, err)
	require.Equal(t, "linux-amd64.tar.gz", selected.Name, "the exact selector has higher specificity than linux/* and */amd64")
}

func TestSupportedArtifactInventoryRequiresAtLeastOneSupportedFormat(t *testing.T) {
	unsupported := releaseArtifact{Name: "linux.tar.xz", OS: ptr("linux"), Arch: ptr("amd64"), Format: ptr("tar.xz"), Size: ptr(uint64(1)), URL: ptr("https://dl.example/linux.tar.xz")}
	release, err := parseDBCRelease(validRelease("1.0.0", unsupported), testProject)
	require.NoError(t, err)
	require.ErrorContains(t, validateSupportedArtifactSet(release), "no dbc artifact with a supported tar.gz or tgz format")
}

func TestParseReleaseHasNoUnsignedFallback(t *testing.T) {
	_, err := parseRelease([]byte(`{"_type":"https://in-toto.io/Statement/v1","subject":[],"predicateType":"https://packslip.dev/release/v1","predicate":{}}`), testProject)
	require.Error(t, err)
}
