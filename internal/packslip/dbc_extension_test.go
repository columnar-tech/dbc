// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package packslip

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDBCReleaseExtensionRequiresExactDeclaration(t *testing.T) {
	tests := []struct {
		name    string
		raw     json.RawMessage
		missing bool
		want    string
	}{
		{name: "missing", missing: true, want: "missing predicate.extensions.dbc"},
		{name: "null", raw: json.RawMessage("null"), want: "non-null object"},
		{name: "malformed", raw: json.RawMessage("{"), want: "invalid packslip predicate.extensions.dbc declaration"},
		{name: "array", raw: json.RawMessage("[]"), want: "non-null object"},
		{name: "unknown field", raw: json.RawMessage(`{"schema_version":1,"driver_id":"driver","future":true}`), want: "unknown field"},
		{name: "unknown schema", raw: json.RawMessage(`{"schema_version":2,"driver_id":"driver"}`), want: "unsupported dbc release extension schema_version"},
		{name: "missing schema", raw: json.RawMessage(`{"driver_id":"driver"}`), want: "schema_version 0"},
		{name: "invalid id", raw: json.RawMessage(`{"schema_version":1,"driver_id":"../driver"}`), want: "invalid dbc release driver_id"},
		{name: "reserved id", raw: json.RawMessage(`{"schema_version":1,"driver_id":"CON"}`), want: "reserved by Windows"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release, err := parseRelease(validRelease("1.0.0"), testProject)
			require.NoError(t, err)
			if test.missing {
				release.predicate.Extensions = nil
			} else {
				release.predicate.Extensions["dbc"] = test.raw
			}
			err = validateDBCReleaseExtensions(release)
			require.ErrorContains(t, err, test.want)
		})
	}

	release, err := parseRelease(validRelease("1.0.0"), testProject)
	require.NoError(t, err)
	require.NoError(t, validateDBCReleaseExtensions(release))
	require.Equal(t, "driver", release.driverID)
}

func TestDBCArtifactExtensionRequiresExactPackageMarker(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{name: "null", raw: json.RawMessage("null"), want: "non-null object"},
		{name: "malformed", raw: json.RawMessage("{"), want: "invalid packslip artifact"},
		{name: "array", raw: json.RawMessage("[]"), want: "non-null object"},
		{name: "unknown field", raw: json.RawMessage(`{"package_version":2,"future":true}`), want: "unknown field"},
		{name: "missing package version", raw: json.RawMessage(`{}`), want: "package_version 0"},
		{name: "unsupported package version", raw: json.RawMessage(`{"package_version":1}`), want: "unsupported dbc package_version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release, err := parseRelease(validRelease("1.0.0"), testProject)
			require.NoError(t, err)
			release.predicate.Artifacts[0].Extensions["dbc"] = test.raw
			err = validateDBCReleaseExtensions(release)
			require.ErrorContains(t, err, test.want)
		})
	}

	release, err := parseRelease(validRelease("1.0.0"), testProject)
	require.NoError(t, err)
	require.NoError(t, validateDBCReleaseExtensions(release))
	require.Equal(t, 2, release.predicate.Artifacts[0].dbcPackageVersion)
}

func TestDBCArtifactEligibilityExcludesUnrelatedArchivesFromTargetsAndAmbiguity(t *testing.T) {
	format := "tar.gz"
	selected := releaseArtifact{
		Name: "dbc-linux.tar.gz", OS: ptr("linux"), Arch: ptr("amd64"), LibC: ptr("gnu"),
		Size: ptr(uint64(10)), Format: &format, URL: ptr("https://dl.example/dbc-linux.tar.gz"),
		Extensions: map[string]json.RawMessage{"dbc": mustJSON(dbcArtifactExtension{PackageVersion: 2})},
	}
	other := selected
	other.Name = "unrelated-windows.tar.gz"
	other.OS = ptr("windows")
	other.URL = ptr("https://dl.example/unrelated-windows.tar.gz")
	other.Extensions = map[string]json.RawMessage{"other-consumer": json.RawMessage(`true`)}
	unsupported := selected
	unsupported.Name = "dbc-linux.zip"
	unsupported.Format = ptr("zip")
	unsupported.URL = ptr("https://dl.example/dbc-linux.zip")

	release, err := parseRelease(validRelease("1.0.0", selected, other, unsupported), testProject)
	require.NoError(t, err)
	require.NoError(t, validateDBCReleaseExtensions(release))
	require.NoError(t, validateSupportedArtifactSet(release))
	targets, err := deriveConcreteTargets(release)
	require.NoError(t, err)
	require.Equal(t, []Target{{OS: "linux", Arch: "amd64", LibC: "gnu"}}, targets,
		"only dbc-declared supported archives seed concrete targets")
	chosen, err := selectArtifact(release, targets[0])
	require.NoError(t, err, "unrelated archive must not create a selection tie")
	require.Equal(t, "dbc-linux.tar.gz", chosen.Name)
	_, err = selectArtifact(release, Target{OS: "windows", Arch: "amd64"})
	require.ErrorIs(t, err, ErrReleaseNotFound, "non-dbc artifacts must not be selected")
}

func TestDBCReleaseRequiresAtLeastOneSupportedDeclaredPackage(t *testing.T) {
	format := "tar.gz"
	nonDBC := releaseArtifact{
		Name: "linux.tar.gz", OS: ptr("linux"), Arch: ptr("amd64"), Size: ptr(uint64(1)), Format: &format, URL: ptr("https://dl.example/linux.tar.gz"),
		Extensions: map[string]json.RawMessage{"other-consumer": json.RawMessage(`true`)},
	}
	release, err := parseRelease(validRelease("1.0.0", nonDBC), testProject)
	require.NoError(t, err)
	require.NoError(t, validateDBCReleaseExtensions(release))
	require.ErrorContains(t, validateSupportedArtifactSet(release), "no dbc artifact")
	_, err = deriveConcreteTargets(release)
	require.ErrorContains(t, err, "no supported artifact")
}
