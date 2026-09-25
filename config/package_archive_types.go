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

package config

import (
	"errors"
	"fmt"
	"github.com/Masterminds/semver/v3"
	"os"
	"sync"
)

const (
	installReceiptName               = "dbc-install-receipt.json"
	legacyPackageManifestName        = "MANIFEST"
	packageV2MetadataName            = "dbc-package.toml"
	maxPackageMetadataSize           = 1 << 20
	registrationFingerprintAlgorithm = "sha256"
	registrationFingerprintVersion   = 1
)

// ExpectedPackageMetadata describes the resolution that selected an archive.
// Hashes use the canonical form "sha256:<lowercase hex>". ArchiveSize is the
// optional size of the compressed archive, not the extracted library. A
// nonzero size is treated as present for compatibility with existing callers;
// ArchiveSizePresent allows callers to explicitly require a zero-byte size.
type ExpectedPackageMetadata struct {
	ID                 string
	Version            string
	PackageVersion     int
	Platform           string
	SourceType         string
	SourceIdentity     string
	ArchiveHash        string
	ArchiveSize        int64
	ArchiveSizePresent bool
}

func (expected ExpectedPackageMetadata) hasExpectedArchiveSize() bool {
	return expected.ArchiveSizePresent || expected.ArchiveSize != 0
}

// InstallReceipt records the resolved source and the two distinct artifact
// digests. ArchiveHash covers the compressed package; InstalledLibraryHash
// covers the extracted file referenced by [Files].driver.
type InstallReceipt struct {
	SourceType                       string `json:"source_type"`
	SourceIdentity                   string `json:"source_identity"`
	DriverID                         string `json:"driver_id"`
	DriverVersion                    string `json:"driver_version"`
	Platform                         string `json:"platform"`
	PackageVersion                   int    `json:"package_version"`
	ArchiveHash                      string `json:"archive_hash"`
	ArchiveSize                      int64  `json:"archive_size"`
	InstalledLibrary                 string `json:"installed_library,omitempty"`
	InstalledLibraryHash             string `json:"installed_library_hash,omitempty"`
	RegistrationFingerprintAlgorithm string `json:"registration_fingerprint_algorithm,omitempty"`
	RegistrationFingerprintVersion   int    `json:"registration_fingerprint_version,omitempty"`
	RegistrationFingerprint          string `json:"registration_fingerprint,omitempty"`
}

// InstallOptions supplies verification that must finish before an installation
// is made visible to the runtime driver manager.
type InstallOptions struct {
	Verify func(stagingDir string, manifest Manifest) error
}

// PackageValidation contains values measured while validating a package
// archive. VerifiedLibraryHash is empty when the package has no separate
// driver library file.
type PackageValidation struct {
	VerifiedLibraryHash              string
	ArchiveHash                      string
	ArchiveSize                      int64
	PackageVersion                   int
	Registration                     DriverInfo
	RegistrationFingerprintAlgorithm string
	RegistrationFingerprintVersion   int
	RegistrationFingerprint          string
	registrationSharedIdentity       registrationSharedIdentity
	Prepared                         *PreparedPackage
}

// PreparedPackage owns a validated package payload that has not yet been
// published to the runtime. Its fields are intentionally private: callers may
// only pass it back to EnsurePackage or close it.
type PreparedPackage struct {
	mu         sync.Mutex
	root       string
	runtimeID  string
	requested  ExpectedPackageMetadata
	expected   ExpectedPackageMetadata
	workDir    string
	payloadDir string
	finalDir   string
	manifest   Manifest
	used       bool
	closed     bool
	closeErr   error
}

// Close removes the private prepared-package workspace. It is safe to call
// more than once.
func (p *PreparedPackage) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return p.closeErr
	}
	p.closed = true
	if err := os.RemoveAll(p.workDir); err != nil {
		p.closeErr = fmt.Errorf("could not remove prepared package workspace: %w", err)
	}
	return p.closeErr
}

// MatchesExpected reports whether this unconsumed prepared payload was
// validated for the supplied expected package metadata. It also accepts the
// finalized metadata whose archive hash, size, and package version were
// measured while preparing the package.
func (p *PreparedPackage) MatchesExpected(expected ExpectedPackageMetadata) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.used {
		return false
	}
	normalized, err := normalizePackageInstallMetadata(p.runtimeID, expected)
	if err != nil {
		return false
	}
	return p.requested == normalized || p.expected == normalized
}

func (p *PreparedPackage) claim(root, runtimeID string, expected ExpectedPackageMetadata) error {
	if p == nil {
		return errors.New("prepared package is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("prepared package is closed")
	}
	if p.used {
		return errors.New("prepared package has already been used")
	}
	if p.root != root || p.runtimeID != runtimeID || (p.requested != expected && p.expected != expected) {
		return errors.New("prepared package does not match the requested installation")
	}
	p.used = true
	return nil
}

type registrationSharedIdentity struct {
	Kind              string `json:"kind"`
	PackageFile       string `json:"package_file,omitempty"`
	ExternalReference string `json:"external_reference,omitempty"`
}

type runtimeRegistrationFingerprintV1 struct {
	SchemaVersion int                        `json:"schema_version"`
	ID            string                     `json:"id"`
	Platform      string                     `json:"platform"`
	Source        string                     `json:"source"`
	Name          string                     `json:"name"`
	Publisher     string                     `json:"publisher"`
	License       string                     `json:"license"`
	Entrypoint    string                     `json:"entrypoint"`
	Version       *string                    `json:"version"`
	ADBCVersion   *string                    `json:"adbc_version"`
	Supported     []string                   `json:"supported_features"`
	Unsupported   []string                   `json:"unsupported_features"`
	Shared        registrationSharedIdentity `json:"shared"`
}

type packageManifest struct {
	manifest Manifest
	id       string
	platform string
	v2       bool
}

type packageManifestV2Wire struct {
	PackageVersion  int64  `toml:"package_version"`
	ManifestVersion *int64 `toml:"manifest_version"`
	ID              string `toml:"id"`
	Name            string `toml:"name"`
	Version         string `toml:"version"`
	Platform        string `toml:"platform"`
	Driver          struct {
		Entrypoint string `toml:"entrypoint"`
	} `toml:"Driver"`
	Files struct {
		Driver string `toml:"driver"`
	} `toml:"Files"`
}

// This is the legacy package wire format. It deliberately remains separate
// from runtimeManifestWire: Driver.shared in a runtime ADBC manifest is a load
// path, while Files.driver in a package manifest is an archive member name.
type legacyPackageManifestWire struct {
	ManifestVersion int32           `toml:"manifest_version"`
	Name            string          `toml:"name"`
	Publisher       string          `toml:"publisher"`
	License         string          `toml:"license"`
	Version         *semver.Version `toml:"version"`
	Source          string          `toml:"source"`
	AdbcInfo        struct {
		Version  *semver.Version `toml:"version"`
		Features struct {
			Supported   []string `toml:"supported,omitempty"`
			Unsupported []string `toml:"unsupported,omitempty"`
		} `toml:"features,omitempty"`
	} `toml:"ADBC"`
	Driver struct {
		Entrypoint string `toml:"entrypoint,omitempty"`
		Shared     any    `toml:"shared"`
	} `toml:"Driver"`
	Files struct {
		Driver    string `toml:"driver,omitempty"`
		Signature string `toml:"signature,omitempty"`
	} `toml:"Files,omitempty"`
	PostInstall struct {
		Messages []string `toml:"messages,inline,omitempty"`
	} `toml:"PostInstall,omitempty"`
}
