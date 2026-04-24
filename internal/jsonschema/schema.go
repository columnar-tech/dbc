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

// Package jsonschema defines the versioned JSON output schema for the dbc CLI.
// It is the single source of truth for every --json payload emitted by the CLI
// and consumed by the Rust GUI. Field names MUST NOT be renamed without updating
// both consumers.
package jsonschema

import "encoding/json"

// SchemaVersion is the current JSON schema version. Increment when making
// backward-incompatible changes to any payload type.
const SchemaVersion = 1

// Envelope is the top-level wrapper for every structured JSON response emitted
// by the CLI. The payload field contains a type-specific JSON object identified
// by the kind field.
type Envelope struct {
	// SchemaVersion identifies the schema revision for forward-compatibility checks.
	SchemaVersion int `json:"schema_version"`
	// Kind names the payload type (e.g. "install.status", "search.response").
	Kind string `json:"kind"`
	// Payload is the raw JSON-encoded type-specific data.
	Payload json.RawMessage `json:"payload"`
}

// -----------------------------------------------------------------------------
// Install
// -----------------------------------------------------------------------------

// InstallStatus is the final JSON payload emitted after a driver installation
// attempt. It corresponds to the inline struct in cmd/dbc/install.go.
type InstallStatus struct {
	// Status is the outcome: "installed", "already installed", or "error".
	Status string `json:"status"`
	// Driver is the driver identifier (e.g. "snowflake").
	Driver string `json:"driver"`
	// Version is the installed driver version string.
	Version string `json:"version"`
	// Location is the filesystem path where the driver was installed.
	Location string `json:"location"`
	// Message is an optional post-install notice provided by the driver manifest.
	Message string `json:"message,omitempty"`
	// Conflict describes any pre-existing driver that was replaced, if applicable.
	Conflict string `json:"conflict,omitempty"`
	// Checksum is the hex-encoded checksum of the installed artifact (added for T7).
	Checksum string `json:"checksum,omitempty"`
}

// InstallProgressEvent is a single line in the NDJSON progress stream emitted
// during a driver installation. Clients should consume a stream of these events
// until install.complete is received.
type InstallProgressEvent struct {
	// Event identifies the progress step. Valid values:
	// "download.start", "download.progress", "download.complete",
	// "extract.start", "extract.complete",
	// "verify.start", "verify.complete", "verify.checksum.ok", "verify.checksum.mismatch",
	// "manifest.create", "install.complete".
	Event string `json:"event"`
	// Driver is the driver identifier being installed.
	Driver string `json:"driver"`
	// Bytes is the number of bytes transferred so far (download events only).
	Bytes int64 `json:"bytes,omitempty"`
	// Total is the expected total byte count (download events only).
	Total int64 `json:"total,omitempty"`
	// Checksum is the computed or expected checksum value (verify events only).
	Checksum string `json:"checksum,omitempty"`
}

// -----------------------------------------------------------------------------
// Uninstall
// -----------------------------------------------------------------------------

// UninstallStatus is the JSON payload emitted after a driver uninstallation.
// It corresponds to the inline format in cmd/dbc/uninstall.go.
type UninstallStatus struct {
	// Status is the outcome: "success" or "error".
	Status string `json:"status"`
	// Driver is the driver identifier that was uninstalled.
	Driver string `json:"driver"`
}

// -----------------------------------------------------------------------------
// Search
// -----------------------------------------------------------------------------

// SearchDriverBasic is a single driver entry in a non-verbose search response.
// It corresponds to the basic output struct in cmd/dbc/search.go.
type SearchDriverBasic struct {
	// Driver is the driver identifier path.
	Driver string `json:"driver"`
	// Description is a human-readable summary of the driver.
	Description string `json:"description"`
	// Installed lists the installed versions of this driver, if any.
	Installed []string `json:"installed,omitempty"`
	// Registry is the source registry name, if not the default.
	Registry string `json:"registry,omitempty"`
}

// SearchDriverVerbose is a single driver entry in a verbose search response.
// It corresponds to the verbose output struct in cmd/dbc/search.go.
type SearchDriverVerbose struct {
	// Driver is the driver identifier path.
	Driver string `json:"driver"`
	// Description is a human-readable summary of the driver.
	Description string `json:"description"`
	// License is the SPDX license identifier for the driver.
	License string `json:"license"`
	// Registry is the source registry name, if not the default.
	Registry string `json:"registry,omitempty"`
	// InstalledVersions maps platform tuple to a list of installed versions.
	InstalledVersions map[string][]string `json:"installed_versions,omitempty"`
	// AvailableVersions lists all versions available for the current platform.
	AvailableVersions []string `json:"available_versions,omitempty"`
}

// SearchResponse is the top-level JSON payload for the search command. The
// drivers field is raw JSON to accommodate both SearchDriverBasic and
// SearchDriverVerbose slices without a common interface.
type SearchResponse struct {
	// Drivers is the raw JSON array of driver entries (basic or verbose).
	Drivers json.RawMessage `json:"drivers"`
	// Warning is an optional message when some registries were unavailable.
	Warning string `json:"warning,omitempty"`
}

// -----------------------------------------------------------------------------
// Info
// -----------------------------------------------------------------------------

// DriverInfo is the JSON payload for the info command.
// It corresponds to the inline struct in cmd/dbc/info.go.
type DriverInfo struct {
	// Driver is the driver identifier path.
	Driver string `json:"driver"`
	// Version is the latest version string.
	Version string `json:"version"`
	// Title is the human-readable display name of the driver.
	Title string `json:"title"`
	// License is the SPDX license identifier.
	License string `json:"license"`
	// Description is a detailed description of the driver.
	Description string `json:"description"`
	// Packages lists the supported platform tuples.
	Packages []string `json:"packages"`
}

// -----------------------------------------------------------------------------
// Init / Add / Remove (driver list management)
// -----------------------------------------------------------------------------

// InitResponse is the JSON payload emitted when a driver list file is initialised.
type InitResponse struct {
	// DriverListPath is the filesystem path of the created or existing driver list.
	DriverListPath string `json:"driver_list_path"`
	// Created is true when the file was newly created, false if it already existed.
	Created bool `json:"created"`
}

// AddResponseDriver carries the driver entry that was appended to the driver list.
type AddResponseDriver struct {
	// Name is the driver identifier.
	Name string `json:"name"`
	// VersionConstraint is the optional semver constraint that was recorded.
	VersionConstraint string `json:"version_constraint,omitempty"`
}

// AddResponse is the JSON payload emitted after adding drivers to a driver list.
type AddResponse struct {
	// DriverListPath is the filesystem path of the driver list that was modified.
	DriverListPath string `json:"driver_list_path"`
	// Drivers lists every driver entry that was added or updated.
	Drivers []AddResponseDriver `json:"drivers"`
}

// RemoveResponseDriver carries the driver entry that was removed from the list.
type RemoveResponseDriver struct {
	// Name is the driver identifier.
	Name string `json:"name"`
}

// RemoveResponse is the JSON payload emitted after removing a driver from a list.
type RemoveResponse struct {
	// DriverListPath is the filesystem path of the driver list that was modified.
	DriverListPath string `json:"driver_list_path"`
	// Driver is the entry that was removed.
	Driver RemoveResponseDriver `json:"driver"`
}

// -----------------------------------------------------------------------------
// Sync
// -----------------------------------------------------------------------------

// SyncProgressEvent is a single NDJSON line in the sync progress stream.
type SyncProgressEvent struct {
	// Phase is the current sync step: "resolving", "downloading", "verifying", or "installed".
	Phase string `json:"phase"`
	// Driver is the driver identifier being synced.
	Driver string `json:"driver"`
	// Bytes is the number of bytes transferred so far (downloading phase only).
	Bytes int64 `json:"bytes,omitempty"`
	// Total is the expected total byte count (downloading phase only).
	Total int64 `json:"total,omitempty"`
	// Version is the resolved version string (available after resolving phase).
	Version string `json:"version,omitempty"`
}

// SyncedDriver records a driver that was successfully installed or skipped during sync.
type SyncedDriver struct {
	// Name is the driver identifier.
	Name string `json:"name"`
	// Version is the version that was installed or already present.
	Version string `json:"version"`
}

// SyncError records a driver that failed to install during sync.
type SyncError struct {
	// Name is the driver identifier.
	Name string `json:"name"`
	// Error is a human-readable description of the failure.
	Error string `json:"error"`
}

// SyncStatus is the final JSON payload emitted after a sync operation completes.
type SyncStatus struct {
	// Installed lists drivers that were newly installed.
	Installed []SyncedDriver `json:"installed"`
	// Skipped lists drivers that were already present and required no action.
	Skipped []SyncedDriver `json:"skipped"`
	// Errors lists drivers that failed to install.
	Errors []SyncError `json:"errors"`
}

// -----------------------------------------------------------------------------
// Auth
// -----------------------------------------------------------------------------

// AuthDeviceCodeEvent is the JSON payload emitted during OAuth device-code flow.
// The CLI emits this once so the user can open a browser to complete auth.
type AuthDeviceCodeEvent struct {
	// VerificationURI is the URL the user should visit to authorise the device.
	VerificationURI string `json:"verification_uri"`
	// VerificationURIComplete is the URL with the user code pre-filled.
	VerificationURIComplete string `json:"verification_uri_complete"`
	// UserCode is the short code the user must enter at the verification URI.
	UserCode string `json:"user_code"`
	// ExpiresIn is the number of seconds until the code expires.
	ExpiresIn int `json:"expires_in"`
	// Interval is the minimum polling interval in seconds.
	Interval int `json:"interval"`
}

// AuthLoginResponse is the JSON payload emitted after an auth login attempt.
type AuthLoginResponse struct {
	// Status is the outcome: "success" or "failed".
	Status string `json:"status"`
	// Registry is the registry URL that was authenticated against.
	Registry string `json:"registry"`
	// Message is an optional detail string (e.g. failure reason).
	Message string `json:"message,omitempty"`
}

// AuthLogoutResponse is the JSON payload emitted after an auth logout.
type AuthLogoutResponse struct {
	// Status is the outcome: "success" or "error".
	Status string `json:"status"`
	// Registry is the registry URL from which credentials were removed.
	Registry string `json:"registry"`
}

// AuthLicenseInstallResponse is the JSON payload emitted after installing a license file.
type AuthLicenseInstallResponse struct {
	// Status is the outcome: "success" or "error".
	Status string `json:"status"`
	// LicensePath is the filesystem path where the license was written.
	LicensePath string `json:"license_path"`
}

// AuthRegistryStatus describes the authentication state for a single registry.
type AuthRegistryStatus struct {
	// URL is the registry URL.
	URL string `json:"url"`
	// Authenticated indicates whether valid credentials exist for this registry.
	Authenticated bool `json:"authenticated"`
	// AuthType is the credential type in use: "oauth" or "api_key". Empty when not authenticated.
	AuthType string `json:"auth_type,omitempty"`
	// LicenseValid indicates whether a valid Columnar license is present.
	LicenseValid bool `json:"license_valid"`
}

// AuthStatus is the JSON payload for the auth status command. It summarises
// the authentication state across all known registries.
type AuthStatus struct {
	// Registries lists the status for each known registry.
	Registries []AuthRegistryStatus `json:"registries"`
}

// -----------------------------------------------------------------------------
// Error
// -----------------------------------------------------------------------------

// ErrorResponse is the JSON payload emitted when a command fails with an error.
// It is used in place of all other payloads when Kind == "error".
type ErrorResponse struct {
	// Code is a machine-readable error identifier (e.g. "not_found", "permission_denied").
	Code string `json:"code"`
	// Message is a human-readable error description.
	Message string `json:"message"`
	// OwnerPID is the PID of the process holding a lock, when applicable.
	OwnerPID int `json:"owner_pid,omitempty"`
}
