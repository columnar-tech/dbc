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

package jsonschema_test

import (
	"encoding/json"
	"testing"

	"github.com/columnar-tech/dbc/internal/jsonschema"
)

func TestSchemaVersion(t *testing.T) {
	if jsonschema.SchemaVersion != 1 {
		t.Fatalf("expected SchemaVersion == 1, got %d", jsonschema.SchemaVersion)
	}
}

func roundTrip[T any](t *testing.T, v T) T {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	return out
}

func TestEnvelope(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{"key": "value"})
	v := jsonschema.Envelope{
		SchemaVersion: jsonschema.SchemaVersion,
		Kind:          "test.event",
		Payload:       json.RawMessage(payload),
	}
	got := roundTrip(t, v)
	if got.SchemaVersion != v.SchemaVersion {
		t.Errorf("SchemaVersion: want %d got %d", v.SchemaVersion, got.SchemaVersion)
	}
	if got.Kind != v.Kind {
		t.Errorf("Kind: want %q got %q", v.Kind, got.Kind)
	}
	if string(got.Payload) != string(v.Payload) {
		t.Errorf("Payload: want %s got %s", v.Payload, got.Payload)
	}
}

func TestEnvelope_JSONFieldNames(t *testing.T) {
	v := jsonschema.Envelope{SchemaVersion: 1, Kind: "k", Payload: json.RawMessage(`{}`)}
	b, _ := json.Marshal(v)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	for _, key := range []string{"schema_version", "kind", "payload"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q", key)
		}
	}
}

func TestInstallStatus(t *testing.T) {
	tests := []struct {
		name string
		in   jsonschema.InstallStatus
	}{
		{
			name: "full",
			in: jsonschema.InstallStatus{
				Status:   "installed",
				Driver:   "snowflake",
				Version:  "1.2.3",
				Location: "/usr/local/lib",
				Message:  "post-install note",
				Conflict: "snowflake (version: 1.0.0)",
				Checksum: "abc123",
			},
		},
		{
			name: "omitempty fields absent",
			in: jsonschema.InstallStatus{
				Status:   "installed",
				Driver:   "sqlite",
				Version:  "0.1.0",
				Location: "/home/user/.local",
			},
		},
		{
			name: "already installed",
			in: jsonschema.InstallStatus{
				Status:   "already installed",
				Driver:   "duckdb",
				Version:  "2.0.0",
				Location: "/opt/drivers",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := roundTrip(t, tc.in)
			if got != tc.in {
				t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", tc.in, got)
			}
		})
	}
}

func TestInstallStatus_OmitemptyAbsent(t *testing.T) {
	v := jsonschema.InstallStatus{Status: "installed", Driver: "d", Version: "1", Location: "/x"}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	for _, key := range []string{"message", "conflict", "checksum"} {
		if _, ok := m[key]; ok {
			t.Errorf("omitempty field %q should be absent", key)
		}
	}
}

func TestInstallProgressEvent(t *testing.T) {
	tests := []struct {
		name string
		in   jsonschema.InstallProgressEvent
	}{
		{
			name: "download progress",
			in: jsonschema.InstallProgressEvent{
				Event:  "download.progress",
				Driver: "snowflake",
				Bytes:  512,
				Total:  1024,
			},
		},
		{
			name: "verify checksum ok",
			in: jsonschema.InstallProgressEvent{
				Event:    "verify.checksum.ok",
				Driver:   "duckdb",
				Checksum: "deadbeef",
			},
		},
		{
			name: "install complete",
			in: jsonschema.InstallProgressEvent{
				Event:  "install.complete",
				Driver: "sqlite",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := roundTrip(t, tc.in)
			if got != tc.in {
				t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", tc.in, got)
			}
		})
	}
}

func TestInstallProgressEvent_OmitemptyAbsent(t *testing.T) {
	v := jsonschema.InstallProgressEvent{Event: "install.complete", Driver: "d"}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	for _, key := range []string{"bytes", "total", "checksum"} {
		if _, ok := m[key]; ok {
			t.Errorf("omitempty field %q should be absent", key)
		}
	}
}

func TestUninstallStatus(t *testing.T) {
	tests := []struct {
		name string
		in   jsonschema.UninstallStatus
	}{
		{name: "success", in: jsonschema.UninstallStatus{Status: "success", Driver: "snowflake"}},
		{name: "error", in: jsonschema.UninstallStatus{Status: "error", Driver: "duckdb"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := roundTrip(t, tc.in)
			if got != tc.in {
				t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", tc.in, got)
			}
		})
	}
}

func TestSearchDriverBasic(t *testing.T) {
	v := jsonschema.SearchDriverBasic{
		Driver:      "snowflake",
		Description: "Snowflake ADBC driver",
		Installed:   []string{"1.0.0", "1.1.0"},
		Registry:    "private",
	}
	got := roundTrip(t, v)
	if got.Driver != v.Driver || got.Description != v.Description || got.Registry != v.Registry {
		t.Errorf("field mismatch: %+v", got)
	}
	if len(got.Installed) != len(v.Installed) {
		t.Errorf("Installed len: want %d got %d", len(v.Installed), len(got.Installed))
	}
}

func TestSearchDriverBasic_OmitemptyAbsent(t *testing.T) {
	v := jsonschema.SearchDriverBasic{Driver: "d", Description: "desc"}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	for _, key := range []string{"installed", "registry"} {
		if _, ok := m[key]; ok {
			t.Errorf("omitempty field %q should be absent", key)
		}
	}
}

func TestSearchDriverVerbose(t *testing.T) {
	v := jsonschema.SearchDriverVerbose{
		Driver:      "duckdb",
		Description: "DuckDB ADBC driver",
		License:     "MIT",
		Registry:    "public",
		InstalledVersions: map[string][]string{
			"linux-amd64": {"1.0.0"},
		},
		AvailableVersions: []string{"1.0.0", "1.1.0"},
	}
	b, _ := json.Marshal(v)
	var got jsonschema.SearchDriverVerbose
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Driver != v.Driver || got.License != v.License {
		t.Errorf("field mismatch: %+v", got)
	}
	if len(got.InstalledVersions["linux-amd64"]) != 1 {
		t.Errorf("InstalledVersions mismatch")
	}
	if len(got.AvailableVersions) != 2 {
		t.Errorf("AvailableVersions len: want 2 got %d", len(got.AvailableVersions))
	}
}

func TestSearchResponse(t *testing.T) {
	drivers, _ := json.Marshal([]jsonschema.SearchDriverBasic{
		{Driver: "d", Description: "desc"},
	})
	v := jsonschema.SearchResponse{
		Drivers: json.RawMessage(drivers),
		Warning: "some registry unavailable",
	}
	got := roundTrip(t, v)
	if string(got.Drivers) != string(v.Drivers) {
		t.Errorf("Drivers mismatch")
	}
	if got.Warning != v.Warning {
		t.Errorf("Warning: want %q got %q", v.Warning, got.Warning)
	}
}

func TestSearchResponse_OmitemptyAbsent(t *testing.T) {
	drivers, _ := json.Marshal([]jsonschema.SearchDriverBasic{})
	v := jsonschema.SearchResponse{Drivers: json.RawMessage(drivers)}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	if _, ok := m["warning"]; ok {
		t.Errorf("omitempty field 'warning' should be absent")
	}
}

func TestDriverInfo(t *testing.T) {
	v := jsonschema.DriverInfo{
		Driver:      "snowflake",
		Version:     "1.2.3",
		Title:       "Snowflake Driver",
		License:     "Apache-2.0",
		Description: "Connects to Snowflake via ADBC",
		Packages:    []string{"linux-amd64", "darwin-arm64"},
	}
	b, _ := json.Marshal(v)
	var got jsonschema.DriverInfo
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Driver != v.Driver || got.Version != v.Version || got.Title != v.Title ||
		got.License != v.License || got.Description != v.Description {
		t.Errorf("field mismatch: %+v", got)
	}
	if len(got.Packages) != 2 {
		t.Errorf("Packages len: want 2 got %d", len(got.Packages))
	}
}

func TestDriverInfo_JSONFieldNames(t *testing.T) {
	v := jsonschema.DriverInfo{Driver: "d", Version: "1", Title: "t", License: "MIT", Description: "desc", Packages: []string{}}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	for _, key := range []string{"driver", "version", "title", "license", "description", "packages"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q", key)
		}
	}
}

func TestInitResponse(t *testing.T) {
	tests := []struct {
		name string
		in   jsonschema.InitResponse
	}{
		{name: "created", in: jsonschema.InitResponse{DriverListPath: "/path/to/dbc.toml", Created: true}},
		{name: "existed", in: jsonschema.InitResponse{DriverListPath: "/path/to/dbc.toml", Created: false}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := roundTrip(t, tc.in)
			if got != tc.in {
				t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", tc.in, got)
			}
		})
	}
}

func TestAddResponse(t *testing.T) {
	v := jsonschema.AddResponse{
		DriverListPath: "/proj/dbc.toml",
		Driver: jsonschema.AddResponseDriver{
			Name:              "snowflake",
			VersionConstraint: ">=1.0.0",
		},
	}
	got := roundTrip(t, v)
	if got.DriverListPath != v.DriverListPath {
		t.Errorf("DriverListPath mismatch")
	}
	if got.Driver.Name != v.Driver.Name || got.Driver.VersionConstraint != v.Driver.VersionConstraint {
		t.Errorf("Driver mismatch: %+v", got.Driver)
	}
}

func TestAddResponse_OmitemptyAbsent(t *testing.T) {
	v := jsonschema.AddResponseDriver{Name: "d"}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	if _, ok := m["version_constraint"]; ok {
		t.Errorf("omitempty field 'version_constraint' should be absent")
	}
}

func TestRemoveResponse(t *testing.T) {
	v := jsonschema.RemoveResponse{
		DriverListPath: "/proj/dbc.toml",
		Driver:         jsonschema.RemoveResponseDriver{Name: "snowflake"},
	}
	got := roundTrip(t, v)
	if got.DriverListPath != v.DriverListPath || got.Driver.Name != v.Driver.Name {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestSyncProgressEvent(t *testing.T) {
	tests := []struct {
		name string
		in   jsonschema.SyncProgressEvent
	}{
		{
			name: "downloading",
			in:   jsonschema.SyncProgressEvent{Phase: "downloading", Driver: "duckdb", Bytes: 100, Total: 200},
		},
		{
			name: "installed",
			in:   jsonschema.SyncProgressEvent{Phase: "installed", Driver: "duckdb", Version: "1.2.3"},
		},
		{
			name: "resolving no optional",
			in:   jsonschema.SyncProgressEvent{Phase: "resolving", Driver: "sqlite"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := roundTrip(t, tc.in)
			if got != tc.in {
				t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", tc.in, got)
			}
		})
	}
}

func TestSyncProgressEvent_OmitemptyAbsent(t *testing.T) {
	v := jsonschema.SyncProgressEvent{Phase: "resolving", Driver: "d"}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	for _, key := range []string{"bytes", "total", "version"} {
		if _, ok := m[key]; ok {
			t.Errorf("omitempty field %q should be absent", key)
		}
	}
}

func TestSyncStatus(t *testing.T) {
	v := jsonschema.SyncStatus{
		Installed: []jsonschema.SyncedDriver{{Name: "snowflake", Version: "1.0.0"}},
		Skipped:   []jsonschema.SyncedDriver{{Name: "duckdb", Version: "2.0.0"}},
		Errors:    []jsonschema.SyncError{{Name: "sqlite", Error: "not found"}},
	}
	b, _ := json.Marshal(v)
	var got jsonschema.SyncStatus
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Installed) != 1 || got.Installed[0] != v.Installed[0] {
		t.Errorf("Installed mismatch")
	}
	if len(got.Skipped) != 1 || got.Skipped[0] != v.Skipped[0] {
		t.Errorf("Skipped mismatch")
	}
	if len(got.Errors) != 1 || got.Errors[0] != v.Errors[0] {
		t.Errorf("Errors mismatch")
	}
}

func TestSyncStatus_EmptySlices(t *testing.T) {
	v := jsonschema.SyncStatus{
		Installed: []jsonschema.SyncedDriver{},
		Skipped:   []jsonschema.SyncedDriver{},
		Errors:    []jsonschema.SyncError{},
	}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	for _, key := range []string{"installed", "skipped", "errors"} {
		if _, ok := m[key]; !ok {
			t.Errorf("field %q should be present (not omitempty)", key)
		}
	}
}

func TestAuthDeviceCodeEvent(t *testing.T) {
	v := jsonschema.AuthDeviceCodeEvent{
		VerificationURI:         "https://auth.example.com/activate",
		VerificationURIComplete: "https://auth.example.com/activate?user_code=ABCD-1234",
		UserCode:                "ABCD-1234",
		ExpiresIn:               300,
		Interval:                5,
	}
	got := roundTrip(t, v)
	if got != v {
		t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", v, got)
	}
}

func TestAuthDeviceCodeEvent_JSONFieldNames(t *testing.T) {
	v := jsonschema.AuthDeviceCodeEvent{
		VerificationURI:         "u",
		VerificationURIComplete: "uc",
		UserCode:                "CODE",
		ExpiresIn:               60,
		Interval:                5,
	}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	for _, key := range []string{"verification_uri", "verification_uri_complete", "user_code", "expires_in", "interval"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q", key)
		}
	}
}

func TestAuthLoginResponse(t *testing.T) {
	tests := []struct {
		name string
		in   jsonschema.AuthLoginResponse
	}{
		{name: "success", in: jsonschema.AuthLoginResponse{Status: "success", Registry: "https://reg.example.com"}},
		{name: "failed", in: jsonschema.AuthLoginResponse{Status: "failed", Registry: "https://reg.example.com", Message: "invalid token"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := roundTrip(t, tc.in)
			if got != tc.in {
				t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", tc.in, got)
			}
		})
	}
}

func TestAuthLoginResponse_OmitemptyAbsent(t *testing.T) {
	v := jsonschema.AuthLoginResponse{Status: "success", Registry: "https://r.example.com"}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	if _, ok := m["message"]; ok {
		t.Errorf("omitempty field 'message' should be absent")
	}
}

func TestAuthLogoutResponse(t *testing.T) {
	v := jsonschema.AuthLogoutResponse{Status: "success", Registry: "https://reg.example.com"}
	got := roundTrip(t, v)
	if got != v {
		t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", v, got)
	}
}

func TestAuthLicenseInstallResponse(t *testing.T) {
	v := jsonschema.AuthLicenseInstallResponse{Status: "success", LicensePath: "/home/.config/dbc/columnar.lic"}
	got := roundTrip(t, v)
	if got != v {
		t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", v, got)
	}
}

func TestAuthLicenseInstallResponse_JSONFieldNames(t *testing.T) {
	v := jsonschema.AuthLicenseInstallResponse{Status: "s", LicensePath: "/p"}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	for _, key := range []string{"status", "license_path"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q", key)
		}
	}
}

func TestAuthRegistryStatus(t *testing.T) {
	tests := []struct {
		name string
		in   jsonschema.AuthRegistryStatus
	}{
		{
			name: "oauth authenticated",
			in: jsonschema.AuthRegistryStatus{
				URL:           "https://reg.example.com",
				Authenticated: true,
				AuthType:      "oauth",
				LicenseValid:  true,
			},
		},
		{
			name: "api_key authenticated",
			in: jsonschema.AuthRegistryStatus{
				URL:           "https://private.example.com",
				Authenticated: true,
				AuthType:      "api_key",
				LicenseValid:  false,
			},
		},
		{
			name: "not authenticated",
			in: jsonschema.AuthRegistryStatus{
				URL:           "https://reg.example.com",
				Authenticated: false,
				LicenseValid:  false,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := roundTrip(t, tc.in)
			if got != tc.in {
				t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", tc.in, got)
			}
		})
	}
}

func TestAuthRegistryStatus_OmitemptyAbsent(t *testing.T) {
	v := jsonschema.AuthRegistryStatus{URL: "https://r.example.com", Authenticated: false, LicenseValid: false}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	if _, ok := m["auth_type"]; ok {
		t.Errorf("omitempty field 'auth_type' should be absent")
	}
}

func TestAuthStatus(t *testing.T) {
	v := jsonschema.AuthStatus{
		Registries: []jsonschema.AuthRegistryStatus{
			{URL: "https://r1.example.com", Authenticated: true, AuthType: "oauth", LicenseValid: true},
			{URL: "https://r2.example.com", Authenticated: false, LicenseValid: false},
		},
	}
	b, _ := json.Marshal(v)
	var got jsonschema.AuthStatus
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Registries) != 2 {
		t.Fatalf("Registries len: want 2 got %d", len(got.Registries))
	}
	if got.Registries[0] != v.Registries[0] || got.Registries[1] != v.Registries[1] {
		t.Errorf("Registries mismatch")
	}
}

func TestErrorResponse(t *testing.T) {
	tests := []struct {
		name string
		in   jsonschema.ErrorResponse
	}{
		{name: "simple", in: jsonschema.ErrorResponse{Code: "not_found", Message: "driver not found"}},
		{name: "with pid", in: jsonschema.ErrorResponse{Code: "locked", Message: "file locked", OwnerPID: 12345}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := roundTrip(t, tc.in)
			if got != tc.in {
				t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", tc.in, got)
			}
		})
	}
}

func TestErrorResponse_OmitemptyAbsent(t *testing.T) {
	v := jsonschema.ErrorResponse{Code: "err", Message: "msg"}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	if _, ok := m["owner_pid"]; ok {
		t.Errorf("omitempty field 'owner_pid' should be absent")
	}
}

func TestSyncedDriver(t *testing.T) {
	v := jsonschema.SyncedDriver{Name: "snowflake", Version: "1.0.0"}
	got := roundTrip(t, v)
	if got != v {
		t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", v, got)
	}
}

func TestSyncError(t *testing.T) {
	v := jsonschema.SyncError{Name: "sqlite", Error: "download failed"}
	got := roundTrip(t, v)
	if got != v {
		t.Errorf("round-trip mismatch:\n want %+v\n  got %+v", v, got)
	}
}
