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
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/Masterminds/semver/v3"
	"github.com/pelletier/go-toml/v2"
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
// size of the compressed archive, not the extracted library.
type ExpectedPackageMetadata struct {
	ID             string
	Version        string
	PackageVersion int
	Platform       string
	SourceType     string
	SourceIdentity string
	ArchiveHash    string
	ArchiveSize    int64
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

func decodePackageManifest(name string, data []byte) (packageManifest, error) {
	switch name {
	case packageV2MetadataName:
		return decodePackageV2Metadata(data)
	case legacyPackageManifestName:
		return decodeLegacyPackageManifest(data)
	default:
		return packageManifest{}, fmt.Errorf("unsupported package metadata filename %q", name)
	}
}

func decodePackageV2Metadata(data []byte) (packageManifest, error) {
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return packageManifest{}, fmt.Errorf("%w: error decoding package v2 metadata: %v", ErrInvalidManifest, err)
	}

	rawVersion, hasDiscriminator := root["package_version"]
	if !hasDiscriminator {
		return packageManifest{}, fmt.Errorf("%w: package_version = 2 is required in %s", ErrInvalidManifest, packageV2MetadataName)
	}
	version, ok := rawVersion.(int64)
	if !ok {
		return packageManifest{}, fmt.Errorf("%w: package_version must be an integer", ErrInvalidManifest)
	}
	if version != 2 {
		return packageManifest{}, fmt.Errorf("%w: package version %d is unsupported", ErrInvalidManifest, version)
	}

	var wire packageManifestV2Wire
	if err := toml.Unmarshal(data, &wire); err != nil {
		return packageManifest{}, fmt.Errorf("%w: error decoding package v2 metadata: %v", ErrInvalidManifest, err)
	}
	if wire.PackageVersion != 2 {
		return packageManifest{}, fmt.Errorf("%w: package_version must be 2", ErrInvalidManifest)
	}
	if wire.ManifestVersion != nil {
		return packageManifest{}, fmt.Errorf("%w: package v2 must not set runtime manifest_version", ErrInvalidManifest)
	}
	if err := validateFlatName(wire.ID); err != nil {
		return packageManifest{}, fmt.Errorf("%w: invalid package id: %v", ErrInvalidManifest, err)
	}
	if strings.TrimSpace(wire.Name) == "" {
		return packageManifest{}, fmt.Errorf("%w: name is required", ErrInvalidManifest)
	}
	if wire.Version == "" {
		return packageManifest{}, fmt.Errorf("%w: version is required", ErrInvalidManifest)
	}
	parsedVersion, err := semver.StrictNewVersion(wire.Version)
	if err != nil {
		return packageManifest{}, fmt.Errorf("%w: version %q must be valid SemVer 2.0.0: %v", ErrInvalidManifest, wire.Version, err)
	}
	if err := validatePlatformIdentifier(wire.Platform); err != nil {
		return packageManifest{}, fmt.Errorf("%w: invalid platform: %v", ErrInvalidManifest, err)
	}
	if strings.TrimSpace(wire.Driver.Entrypoint) == "" {
		return packageManifest{}, fmt.Errorf("%w: Driver.entrypoint is required", ErrInvalidManifest)
	}
	if err := validateFlatName(wire.Files.Driver); err != nil {
		return packageManifest{}, fmt.Errorf("%w: invalid Files.driver: %v", ErrInvalidManifest, err)
	}

	return packageManifest{
		manifest: Manifest{
			PackageVersion:  2,
			PackagePlatform: wire.Platform,
			DriverInfo: DriverInfo{
				ID:      wire.ID,
				Name:    wire.Name,
				Version: parsedVersion,
				Driver: struct {
					Entrypoint string
					Shared     driverMap
				}{Entrypoint: wire.Driver.Entrypoint},
			},
			Files: struct {
				Driver    string `toml:"driver,omitempty"`
				Signature string `toml:"signature,omitempty"`
			}{Driver: wire.Files.Driver},
		},
		id: wire.ID, platform: wire.Platform, v2: true,
	}, nil
}

func decodeLegacyPackageManifest(data []byte) (packageManifest, error) {
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return packageManifest{}, fmt.Errorf("%w: error decoding legacy package manifest: %v", ErrInvalidManifest, err)
	}
	if _, hasPackageVersion := root["package_version"]; hasPackageVersion {
		return packageManifest{}, fmt.Errorf("%w: package_version is not allowed in legacy %s; use %s for package v2 metadata", ErrInvalidManifest, legacyPackageManifestName, packageV2MetadataName)
	}

	var wire legacyPackageManifestWire
	if err := toml.Unmarshal(data, &wire); err != nil {
		return packageManifest{}, fmt.Errorf("%w: error decoding legacy package manifest: %v", ErrInvalidManifest, err)
	}
	if wire.ManifestVersion > currentManifestVersion {
		return packageManifest{}, fmt.Errorf("manifest version %d is unsupported, only %d and lower are supported by this version of dbc", wire.ManifestVersion, currentManifestVersion)
	}
	if strings.TrimSpace(wire.Name) == "" {
		return packageManifest{}, fmt.Errorf("%w: name is required", ErrInvalidManifest)
	}
	if wire.Version == nil {
		return packageManifest{}, fmt.Errorf("%w: version is required", ErrInvalidManifest)
	}
	if wire.Files.Driver != "" {
		if err := validateFlatName(wire.Files.Driver); err != nil {
			return packageManifest{}, fmt.Errorf("%w: invalid Files.driver: %v", ErrInvalidManifest, err)
		}
	}
	shared, err := decodeDriverShared(wire.Driver.Shared, false)
	if err != nil {
		return packageManifest{}, err
	}
	return packageManifest{
		manifest: Manifest{
			DriverInfo: DriverInfo{
				Name:      wire.Name,
				Publisher: wire.Publisher,
				License:   wire.License,
				Version:   wire.Version,
				Source:    wire.Source,
				AdbcInfo:  wire.AdbcInfo,
				Driver: struct {
					Entrypoint string
					Shared     driverMap
				}{Entrypoint: wire.Driver.Entrypoint, Shared: shared},
			},
			Files:       wire.Files,
			PostInstall: wire.PostInstall,
		},
	}, nil
}

func classifyPackageMetadataName(name string) (string, bool, error) {
	for _, expected := range []string{legacyPackageManifestName, packageV2MetadataName} {
		if !strings.EqualFold(name, expected) {
			continue
		}
		if name != expected {
			return "", false, fmt.Errorf("package metadata file must be named exactly %q", expected)
		}
		return expected, true, nil
	}
	return "", false, nil
}

func selectPackageMetadata(metadata map[string][]byte) (string, []byte, error) {
	legacy, hasLegacy := metadata[legacyPackageManifestName]
	v2, hasV2 := metadata[packageV2MetadataName]
	if hasLegacy && hasV2 {
		return "", nil, fmt.Errorf("package archive must contain either %s or %s, not both", legacyPackageManifestName, packageV2MetadataName)
	}
	if hasLegacy {
		return legacyPackageManifestName, legacy, nil
	}
	if hasV2 {
		return packageV2MetadataName, v2, nil
	}
	return "", nil, fmt.Errorf("package archive must contain exactly one of %s or %s", legacyPackageManifestName, packageV2MetadataName)
}

func decodeDriverShared(value any, required bool) (driverMap, error) {
	var result driverMap
	switch shared := value.(type) {
	case string:
		result.defaultPath = shared
	case map[string]any:
		result.platformMap = make(map[string]string, len(shared))
		for platform, rawPath := range shared {
			path, ok := rawPath.(string)
			if !ok {
				return driverMap{}, fmt.Errorf("%w: invalid type for platform %s, expected string", ErrInvalidManifest, platform)
			}
			result.platformMap[platform] = path
		}
	default:
		if required {
			return driverMap{}, fmt.Errorf("%w: invalid type for 'Driver.shared' in manifest, expected string or table", ErrInvalidManifest)
		}
	}
	return result, nil
}

// InstallPackage prepares a package in a private generation directory, verifies
// it, registers its runtime manifest, and then removes a previous managed
// generation when its receipt proves ownership. The downloaded archive remains
// open for the caller.
func InstallPackage(cfg Config, runtimeID string, downloaded *os.File, expected ExpectedPackageMetadata, options InstallOptions) (Manifest, error) {
	return installPackage(cfg, runtimeID, downloaded, expected, options, CreateManifest)
}

// EnsurePackageCallbacks contains the narrow decisions and resources needed
// to atomically reuse or install a selected package. CurrentMatches receives
// the latest registration (nil when absent) once before and, if needed, once
// after preparing a package. Prepare runs at most once and outside the
// driver's install locks. ValidateResult runs before the final lock release;
// if it fails after installation, the installed generation remains committed
// and is described by the returned result. CurrentMatches and ValidateResult
// run synchronously while the driver's install locks are held. They must not
// call InstallPackage, EnsurePackage, UninstallDriver, or another API that
// reacquires a lock for the same driver, because that would deadlock. Prepare
// receives the operation context and should honor its cancellation.
// CurrentMatches may be called twice and must be pure and side-effect-free.
type EnsurePackageCallbacks struct {
	CurrentMatches func(current *DriverInfo) (bool, error)
	// Prepare is called outside install locks only if the initial registration
	// does not match. The returned payload is owned and closed by EnsurePackage.
	Prepare        func(context.Context) (*PreparedPackage, error)
	ValidateResult func(result EnsurePackageResult) error
}

// EnsurePackageResult separates the latest registration observed by a locked
// check (Current), the registration present afterwards or reused on a skip
// (Installed), any registration physically replaced at the primary install
// root (Previous), and an install manifest (nil on a skip).
type EnsurePackageResult struct {
	Skipped   bool
	Current   *DriverInfo
	Installed *DriverInfo
	Previous  *DriverInfo
	Manifest  *Manifest
}

// EnsurePackage checks the effective runtime registration under all
// configured driver install locks. If it does not match, EnsurePackage
// releases those locks, prepares one package, reacquires all locks, and checks
// the latest registration again before deciding to skip or install. This
// prevents package preparation from blocking unrelated driver operations while
// ensuring no stale registration snapshot is used for the final decision.
// EnsurePackage owns and closes the prepared payload, including when phase
// two finds that it is no longer needed. CurrentMatches runs up to twice and
// ValidateResult runs under the driver install locks. Neither callback may
// call InstallPackage, EnsurePackage, UninstallDriver, or another API that
// reacquires a lock for the same driver, because that would deadlock. Prepare
// runs once outside the locks and receives ctx so it can cancel its work.
// CurrentMatches must be pure and side-effect-free because it may run once
// before preparation and again against the latest registration after.
// Result.Current is the most recently inspected registration: it remains the
// phase-one snapshot on preparation or phase-two lock acquisition failure.
// TODO: Review after the prototype whether this callback transaction should remain public or move behind a higher-level/internal API.
func EnsurePackage(ctx context.Context, cfg Config, runtimeID string, expected ExpectedPackageMetadata, callbacks EnsurePackageCallbacks) (EnsurePackageResult, error) {
	return ensurePackageWithLockObserver(ctx, cfg, runtimeID, expected, callbacks, nil)
}

// ensurePackageWithLockObserver is the internal lock-acquisition seam used by
// tests to synchronize with each phase's lock acquisition. The observer is
// called after each successful root acquisition, synchronously while that
// root lock remains held.
func ensurePackageWithLockObserver(ctx context.Context, cfg Config, runtimeID string, expected ExpectedPackageMetadata, callbacks EnsurePackageCallbacks, lockObserver func(root string)) (result EnsurePackageResult, returnErr error) {
	var prepared *PreparedPackage
	defer func() {
		if prepared != nil {
			returnErr = errors.Join(returnErr, prepared.Close())
		}
	}()
	if ctx == nil {
		return result, errors.New("package ensure context is nil")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if callbacks.CurrentMatches == nil {
		return result, errors.New("package current-registration predicate is nil")
	}
	expected, err := normalizePackageInstallMetadata(runtimeID, expected)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	primary, precedenceRoots, err := resolvePackageInstallRoots(cfg)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	releaseLocks, err := acquireDriverInstallLocksWithObserver(ctx, precedenceRoots, runtimeID, lockObserver)
	if err != nil {
		return result, fmt.Errorf("could not lock driver installation: %w", err)
	}
	defer releaseLocks()
	current, matches, err := inspectEnsureCurrent(ctx, cfg, precedenceRoots, runtimeID, callbacks.CurrentMatches)
	result.Current = cloneDriverInfo(current)
	if err != nil {
		return result, err
	}
	if matches {
		return finishEnsureSkip(ctx, result, current, callbacks.ValidateResult)
	}
	releaseLocks()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if callbacks.Prepare == nil {
		return result, errors.New("package preparer is nil")
	}
	prepared, err = callbacks.Prepare(ctx)
	if err != nil {
		return result, fmt.Errorf("could not prepare package: %w", err)
	}
	if prepared == nil {
		return result, errors.New("package preparer returned nil")
	}
	if err := prepared.claim(primary, runtimeID, expected); err != nil {
		return result, err
	}

	phaseTwoRelease, err := acquireDriverInstallLocksWithObserver(ctx, precedenceRoots, runtimeID, lockObserver)
	if err != nil {
		return result, fmt.Errorf("could not relock driver installation: %w", err)
	}
	defer phaseTwoRelease()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	current, matches, err = inspectEnsureCurrent(ctx, cfg, precedenceRoots, runtimeID, callbacks.CurrentMatches)
	result.Current = cloneDriverInfo(current)
	if err != nil {
		return result, err
	}
	if matches {
		return finishEnsureSkip(ctx, result, current, callbacks.ValidateResult)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	manifest, previous, err := installPreparedPackageLocked(cfg, runtimeID, primary, expected, prepared, CreateManifest, cleanupOwnedPackageDirectories)
	if err != nil {
		return result, err
	}
	if previous != nil {
		result.Previous = cloneDriverInfo(previous)
	}
	manifestCopy := manifest
	result.Manifest = &manifestCopy
	installed, err := loadEffectiveInstalledDriver(cfg, precedenceRoots, runtimeID)
	if err != nil {
		return result, fmt.Errorf("package was installed but its runtime registration could not be reloaded: %w", err)
	}
	if installed == nil {
		return result, errors.New("package was installed but no runtime registration was found")
	}
	result.Installed = cloneDriverInfo(installed)
	if callbacks.ValidateResult != nil {
		if err := callbacks.ValidateResult(result); err != nil {
			return result, fmt.Errorf("package was installed but result validation failed: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func inspectEnsureCurrent(ctx context.Context, cfg Config, roots []string, runtimeID string, currentMatches func(*DriverInfo) (bool, error)) (*DriverInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	current, err := loadEffectiveInstalledDriver(cfg, roots, runtimeID)
	if err != nil {
		return nil, false, fmt.Errorf("could not inspect current driver registration: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return current, false, err
	}
	matches, err := currentMatches(cloneDriverInfo(current))
	if err != nil {
		return current, false, fmt.Errorf("could not compare current driver registration: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return current, false, err
	}
	if matches && current == nil {
		return current, false, errors.New("current-registration predicate accepted a missing registration")
	}
	return current, matches, nil
}

func finishEnsureSkip(ctx context.Context, result EnsurePackageResult, current *DriverInfo, validateResult func(EnsurePackageResult) error) (EnsurePackageResult, error) {
	result.Skipped = true
	result.Installed = cloneDriverInfo(current)
	if validateResult != nil {
		if err := validateResult(result); err != nil {
			return result, fmt.Errorf("could not validate ensured driver registration: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func resolvePackageInstallRoots(cfg Config) (string, []string, error) {
	var configured []string
	if cfg.Level == ConfigEnv {
		configured = splitConfigList(cfg.Location)
		if len(configured) == 0 {
			return "", nil, errors.New("ADBC_DRIVER_PATH is empty, must be set to valid path to use")
		}
		for _, root := range configured {
			if root == "" {
				return "", nil, errors.New("ADBC_DRIVER_PATH contains an empty config root")
			}
		}
	} else {
		configured = []string{cfg.Location}
	}
	primary, err := EnsureLocation(cfg)
	if err != nil {
		return "", nil, err
	}
	primary, err = absoluteCleanLocation(primary)
	if err != nil {
		return "", nil, fmt.Errorf("could not resolve primary config root: %w", err)
	}

	roots := make([]string, 0, len(configured))
	seen := make(map[string]struct{}, len(configured))
	for _, configuredRoot := range configured {
		root, err := absoluteCleanLocation(configuredRoot)
		if err != nil {
			return "", nil, fmt.Errorf("could not resolve config root %q: %w", configuredRoot, err)
		}
		identity := packageInstallRootKey(root, runtime.GOOS == "windows")
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		info, err := os.Stat(root)
		if err != nil {
			return "", nil, fmt.Errorf("configured driver config root %s is unavailable: %w", root, err)
		}
		if !info.IsDir() {
			return "", nil, fmt.Errorf("configured driver config root %s is not a directory", root)
		}
		roots = append(roots, root)
	}
	if len(roots) == 0 || roots[0] != primary {
		return "", nil, errors.New("primary driver config root is not present in configured roots")
	}
	return primary, roots, nil
}

func packageInstallRootKey(root string, caseInsensitive bool) string {
	root = filepath.Clean(root)
	if caseInsensitive {
		return strings.ToLower(root)
	}
	return root
}

func absoluteCleanLocation(location string) (string, error) {
	if location == "" {
		return "", errors.New("location is empty")
	}
	abs, err := filepath.Abs(location)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func acquireDriverInstallLocks(ctx context.Context, roots []string, runtimeID string) (func(), error) {
	return acquireDriverInstallLocksWithObserver(ctx, roots, runtimeID, nil)
}

func acquireDriverInstallLocksWithObserver(ctx context.Context, roots []string, runtimeID string, observer func(root string)) (func(), error) {
	lockRoots := slices.Clone(roots)
	// TODO: Use the case-insensitive root identity here to keep Windows lock ordering consistent across path spellings.
	slices.Sort(lockRoots)
	releases := make([]func(), 0, len(lockRoots))
	var releaseOnce sync.Once
	releaseAll := func() {
		releaseOnce.Do(func() {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}
		})
	}
	for _, root := range lockRoots {
		if err := ctx.Err(); err != nil {
			releaseAll()
			return nil, err
		}
		release, err := acquireDriverInstallLockContext(ctx, root, runtimeID)
		if err != nil {
			releaseAll()
			return nil, err
		}
		releases = append(releases, release)
		if observer != nil {
			observer(root)
		}
	}
	return releaseAll, nil
}

func loadEffectiveInstalledDriver(cfg Config, roots []string, runtimeID string) (*DriverInfo, error) {
	if cfg.Level == ConfigEnv {
		for _, root := range roots {
			info, err := loadDriverFromManifest(root, runtimeID)
			if err == nil {
				return cloneDriverInfo(&info), nil
			}
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		return nil, nil
	}
	info, err := loadInstalledDriver(cfg, roots[0], runtimeID)
	if err != nil {
		return nil, err
	}
	return cloneDriverInfo(info), nil
}

func cloneDriverInfo(info *DriverInfo) *DriverInfo {
	if info == nil {
		return nil
	}
	clone := *info
	if info.Version != nil {
		version := *info.Version
		clone.Version = &version
	}
	if info.AdbcInfo.Version != nil {
		version := *info.AdbcInfo.Version
		clone.AdbcInfo.Version = &version
	}
	clone.AdbcInfo.Features.Supported = slices.Clone(info.AdbcInfo.Features.Supported)
	clone.AdbcInfo.Features.Unsupported = slices.Clone(info.AdbcInfo.Features.Unsupported)
	if info.Driver.Shared.platformMap != nil {
		clone.Driver.Shared.platformMap = make(map[string]string, len(info.Driver.Shared.platformMap))
		for platform, path := range info.Driver.Shared.platformMap {
			clone.Driver.Shared.platformMap[platform] = path
		}
	}
	return &clone
}

// PreparePackage verifies and stages an already-downloaded package under its
// selected install root without registering it or changing runtime
// configuration. The returned prepared payload can later be atomically
// published by EnsurePackage; callers must close it if they do not pass it to
// EnsurePackage. The downloaded archive remains open for the caller.
func PreparePackage(cfg Config, runtimeID string, downloaded *os.File, expected ExpectedPackageMetadata, options InstallOptions) (validation PackageValidation, err error) {
	if downloaded == nil {
		return PackageValidation{}, errors.New("package archive is nil")
	}
	requested, err := normalizePackageInstallMetadata(runtimeID, expected)
	if err != nil {
		return PackageValidation{}, err
	}
	root, _, err := resolvePackageInstallRoots(cfg)
	if err != nil {
		return PackageValidation{}, fmt.Errorf("could not resolve package installation root: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return PackageValidation{}, fmt.Errorf("could not resolve package installation root: %w", err)
	}
	workDir, err := os.MkdirTemp(root, ".dbc-install-")
	if err != nil {
		return PackageValidation{}, fmt.Errorf("could not create private package preparation directory: %w", err)
	}
	keepWorkspace := false
	defer func() {
		if !keepWorkspace {
			if cleanupErr := os.RemoveAll(workDir); cleanupErr != nil {
				validation = PackageValidation{}
				err = errors.Join(err, fmt.Errorf("could not remove package preparation directory: %w", cleanupErr))
			}
		}
	}()
	reservedDir, err := os.MkdirTemp(root, ".dbc-package-"+runtimeID+"-")
	if err != nil {
		return PackageValidation{}, fmt.Errorf("could not reserve package generation directory: %w", err)
	}
	if err := os.Remove(reservedDir); err != nil {
		return PackageValidation{}, fmt.Errorf("could not prepare package generation directory: %w", err)
	}
	finalDir := reservedDir
	manifest, payloadDir, sharedIdentity, err := stagePackageArchive(root, runtimeID, finalDir, downloaded, requested, options.Verify, workDir)
	if err != nil {
		return PackageValidation{}, err
	}
	receipt, ok := readPackageReceiptEvidence(workDir, runtimeID, payloadDir)
	if !ok {
		return PackageValidation{}, errors.New("could not read prepared package receipt")
	}
	if manifest.PackageVersion == 2 && requested.ArchiveHash == "" {
		return PackageValidation{}, errors.New("package v2 requires archive hash and size metadata")
	}
	finalExpected := requested
	finalExpected.PackageVersion = manifest.PackageVersion
	finalExpected.ArchiveHash = receipt.ArchiveHash
	finalExpected.ArchiveSize = receipt.ArchiveSize
	prepared := &PreparedPackage{
		root: root, runtimeID: runtimeID, requested: requested, expected: finalExpected,
		workDir: workDir, payloadDir: payloadDir, finalDir: finalDir, manifest: manifest,
	}
	keepWorkspace = true
	return PackageValidation{
		VerifiedLibraryHash: receipt.InstalledLibraryHash,
		ArchiveHash:         receipt.ArchiveHash, ArchiveSize: receipt.ArchiveSize,
		PackageVersion: manifest.PackageVersion, Registration: manifest.DriverInfo,
		RegistrationFingerprintAlgorithm: receipt.RegistrationFingerprintAlgorithm,
		RegistrationFingerprintVersion:   receipt.RegistrationFingerprintVersion,
		RegistrationFingerprint:          receipt.RegistrationFingerprint,
		registrationSharedIdentity:       sharedIdentity,
		Prepared:                         prepared,
	}, nil
}

// SameRuntimeDriverRegistration reports whether two driver registrations have
// the same effective runtime metadata for platform. FilePath is intentionally
// ignored because it identifies where a registration is stored rather than
// what it registers. Versions are compared as canonical semantic strings, so
// original spelling differences normalize while build metadata remains
// significant. Feature lists are compared as sets; the effective shared
// reference remains exact.
func SameRuntimeDriverRegistration(current, candidate DriverInfo, platform string) bool {
	if current.ID != candidate.ID || current.Name != candidate.Name || current.Publisher != candidate.Publisher ||
		current.License != candidate.License || current.Source != candidate.Source ||
		current.Driver.Entrypoint != candidate.Driver.Entrypoint ||
		current.Driver.Shared.Get(platform) != candidate.Driver.Shared.Get(platform) ||
		!semverEquivalent(current.Version, candidate.Version) ||
		!semverEquivalent(current.AdbcInfo.Version, candidate.AdbcInfo.Version) ||
		!slices.Equal(normalizedFeatureSet(current.AdbcInfo.Features.Supported), normalizedFeatureSet(candidate.AdbcInfo.Features.Supported)) ||
		!slices.Equal(normalizedFeatureSet(current.AdbcInfo.Features.Unsupported), normalizedFeatureSet(candidate.AdbcInfo.Features.Unsupported)) {
		return false
	}
	return true
}

// InstallReceiptMatchesRuntimeRegistration verifies that a receipt previously
// obtained through InspectInstallReceipt or InspectDriverInstallReceipt proves
// the current registration's canonical runtime metadata. It does not verify
// library bytes; callers must use VerifyInstallReceiptLibraryIntegrity for
// that separate proof. For package-owned libraries it also requires the
// registered shared basename to match the receipt's installed filename.
func InstallReceiptMatchesRuntimeRegistration(receipt InstallReceipt, current DriverInfo, platform string) bool {
	if !validRegistrationFingerprint(receipt.RegistrationFingerprintAlgorithm, receipt.RegistrationFingerprintVersion, receipt.RegistrationFingerprint) ||
		receipt.DriverID != current.ID || receipt.Platform != platform || current.Version == nil || receipt.DriverVersion != current.Version.String() {
		return false
	}
	sharedPath := current.Driver.Shared.Get(platform)
	var identity registrationSharedIdentity
	if receipt.InstalledLibrary != "" {
		if validateFlatName(receipt.InstalledLibrary) != nil || sharedPath == "" || filepath.Base(filepath.Clean(sharedPath)) != receipt.InstalledLibrary {
			return false
		}
		identity = registrationSharedIdentity{Kind: "package_file", PackageFile: receipt.InstalledLibrary}
	} else {
		identity = registrationSharedIdentity{Kind: "external", ExternalReference: sharedPath}
	}
	fingerprint, err := runtimeRegistrationFingerprint(current, platform, identity)
	if err == nil && fingerprint == receipt.RegistrationFingerprint {
		return true
	}
	// Packages without Files.driver and without an external shared reference
	// retain the historical package-directory runtime fallback. Its generated
	// generation directory is not registration identity.
	if receipt.InstalledLibrary == "" && sharedPath != "" && isGeneratedPackageDirectoryRegistration(current, sharedPath) {
		fingerprint, err = runtimeRegistrationFingerprint(current, platform, registrationSharedIdentity{Kind: "external"})
		return err == nil && fingerprint == receipt.RegistrationFingerprint
	}
	return false
}

// InstallReceiptMatchesExpectedPackage checks the resolved package identity
// recorded by a receipt. A zero expected package version preserves the
// unspecified contract used by registry and local legacy sources; a nonzero
// expectation requires an exact receipt match.
func InstallReceiptMatchesExpectedPackage(receipt InstallReceipt, expected ExpectedPackageMetadata) bool {
	return expected.ID != "" && expected.Version != "" && expected.Platform != "" &&
		expected.SourceType != "" && expected.SourceIdentity != "" && expected.ArchiveHash != "" && expected.ArchiveSize > 0 &&
		receipt.DriverID == expected.ID && receipt.DriverVersion == expected.Version &&
		receipt.Platform == expected.Platform && receipt.SourceType == expected.SourceType &&
		receipt.SourceIdentity == expected.SourceIdentity && receipt.ArchiveHash == expected.ArchiveHash &&
		receipt.ArchiveSize == expected.ArchiveSize &&
		(expected.PackageVersion == 0 || receipt.PackageVersion == expected.PackageVersion)
}

// PackageValidationMatchesRuntimeRegistration compares a current registration
// with evidence returned by PreparePackage. The candidate's shared identity
// is private validation output, so callers cannot label an arbitrary path as
// a package-owned file. Library bytes and receipt identity remain separate
// checks for callers that require them.
func PackageValidationMatchesRuntimeRegistration(current DriverInfo, candidate PackageValidation, platform string) bool {
	if candidate.RegistrationFingerprintAlgorithm != registrationFingerprintAlgorithm ||
		candidate.RegistrationFingerprintVersion != registrationFingerprintVersion ||
		!validRegistrationFingerprint(candidate.RegistrationFingerprintAlgorithm, candidate.RegistrationFingerprintVersion, candidate.RegistrationFingerprint) {
		return false
	}
	if !registrationSharedIdentityValid(candidate.registrationSharedIdentity) ||
		!sharedIdentityMatchesRegistration(candidate.registrationSharedIdentity, current, platform) {
		return false
	}
	candidateFingerprint, err := runtimeRegistrationFingerprint(candidate.Registration, platform, candidate.registrationSharedIdentity)
	if err != nil || candidateFingerprint != candidate.RegistrationFingerprint {
		return false
	}
	currentFingerprint, err := runtimeRegistrationFingerprint(current, platform, candidate.registrationSharedIdentity)
	return err == nil && currentFingerprint == candidate.RegistrationFingerprint
}

func installPackage(cfg Config, runtimeID string, downloaded *os.File, expected ExpectedPackageMetadata, options InstallOptions, registerManifest func(Config, DriverInfo) error) (Manifest, error) {
	return installPackageWithCleanup(cfg, runtimeID, downloaded, expected, options, registerManifest, cleanupOwnedPackageDirectories)
}

func installPackageWithCleanup(cfg Config, runtimeID string, downloaded *os.File, expected ExpectedPackageMetadata, options InstallOptions, registerManifest func(Config, DriverInfo) error, cleanup func(string, string, *DriverInfo, string, DriverInfo) error) (Manifest, error) {
	if downloaded == nil {
		return Manifest{}, errors.New("package archive is nil")
	}
	expected, err := normalizePackageInstallMetadata(runtimeID, expected)
	if err != nil {
		return Manifest{}, err
	}

	loc, err := EnsureLocation(cfg)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not ensure config location: %w", err)
	}
	loc, err = filepath.Abs(loc)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not resolve config location: %w", err)
	}
	releaseLock, err := acquireDriverInstallLock(loc, runtimeID)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not lock driver installation: %w", err)
	}
	defer releaseLock()
	manifest, _, err := installPackageLocked(cfg, runtimeID, loc, downloaded, expected, options, registerManifest, cleanup)
	return manifest, err
}

// installPackageLocked performs the package transaction while the caller owns
// the driver install lock for location/runtimeID. It returns the registration
// that was stored at the target location before this transaction.
func installPackageLocked(cfg Config, runtimeID, loc string, downloaded *os.File, expected ExpectedPackageMetadata, options InstallOptions, registerManifest func(Config, DriverInfo) error, cleanup func(string, string, *DriverInfo, string, DriverInfo) error) (Manifest, *DriverInfo, error) {
	if downloaded == nil {
		return Manifest{}, nil, errors.New("package archive is nil")
	}
	if registerManifest == nil {
		return Manifest{}, nil, errors.New("package manifest registrar is nil")
	}
	if cleanup == nil {
		return Manifest{}, nil, errors.New("package cleanup function is nil")
	}
	var err error
	expected, err = normalizePackageInstallMetadata(runtimeID, expected)
	if err != nil {
		return Manifest{}, nil, err
	}
	loc, err = filepath.Abs(loc)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("could not resolve config location: %w", err)
	}
	previous, err := loadInstalledDriver(cfg, loc, runtimeID)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("could not inspect existing driver registration: %w", err)
	}
	workDir, err := os.MkdirTemp(loc, ".dbc-install-")
	if err != nil {
		return Manifest{}, previous, fmt.Errorf("could not create private installation staging directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	reservedDir, err := os.MkdirTemp(loc, ".dbc-package-"+runtimeID+"-")
	if err != nil {
		return Manifest{}, previous, fmt.Errorf("could not reserve package generation directory: %w", err)
	}
	if err := os.Remove(reservedDir); err != nil {
		return Manifest{}, previous, fmt.Errorf("could not prepare package generation directory: %w", err)
	}
	finalDir := reservedDir

	manifest, payloadDir, _, err := stagePackageArchive(loc, runtimeID, finalDir, downloaded, expected, options.Verify, workDir)
	if err != nil {
		return Manifest{}, previous, err
	}
	return publishStagedPackageLocked(cfg, runtimeID, loc, manifest, payloadDir, finalDir, previous, registerManifest, cleanup)
}

func installPreparedPackageLocked(cfg Config, runtimeID, loc string, expected ExpectedPackageMetadata, prepared *PreparedPackage, registerManifest func(Config, DriverInfo) error, cleanup func(string, string, *DriverInfo, string, DriverInfo) error) (Manifest, *DriverInfo, error) {
	if prepared == nil {
		return Manifest{}, nil, errors.New("prepared package is nil")
	}
	if registerManifest == nil {
		return Manifest{}, nil, errors.New("package manifest registrar is nil")
	}
	if cleanup == nil {
		return Manifest{}, nil, errors.New("package cleanup function is nil")
	}
	var err error
	expected, err = normalizePackageInstallMetadata(runtimeID, expected)
	if err != nil {
		return Manifest{}, nil, err
	}
	loc, err = filepath.Abs(loc)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("could not resolve config location: %w", err)
	}
	prepared.mu.Lock()
	preparedValid := prepared.used && !prepared.closed && prepared.root == loc && prepared.runtimeID == runtimeID &&
		(prepared.requested == expected || prepared.expected == expected)
	manifest := prepared.manifest
	payloadDir := prepared.payloadDir
	finalDir := prepared.finalDir
	prepared.mu.Unlock()
	if !preparedValid {
		return Manifest{}, nil, errors.New("prepared package does not match the requested installation")
	}
	if _, err := os.Lstat(finalDir); err == nil {
		return Manifest{}, nil, fmt.Errorf("package generation path already exists: %s", finalDir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, nil, fmt.Errorf("could not inspect package generation path: %w", err)
	}
	previous, err := loadInstalledDriver(cfg, loc, runtimeID)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("could not inspect existing driver registration: %w", err)
	}
	return publishStagedPackageLocked(cfg, runtimeID, loc, manifest, payloadDir, finalDir, previous, registerManifest, cleanup)
}

func publishStagedPackageLocked(cfg Config, runtimeID, loc string, manifest Manifest, payloadDir, finalDir string, previous *DriverInfo, registerManifest func(Config, DriverInfo) error, cleanup func(string, string, *DriverInfo, string, DriverInfo) error) (Manifest, *DriverInfo, error) {
	if err := os.Rename(payloadDir, finalDir); err != nil {
		return Manifest{}, previous, fmt.Errorf("could not publish verified package generation: %w", err)
	}
	if err := registerManifest(cfg, manifest.DriverInfo); err != nil {
		var rollbackErr *manifestRollbackError
		if errors.As(err, &rollbackErr) {
			return Manifest{}, previous, fmt.Errorf("could not register driver manifest; preserving verified package at %s: %w", finalDir, err)
		}
		if removeErr := os.RemoveAll(finalDir); removeErr != nil {
			return Manifest{}, previous, fmt.Errorf("could not register driver manifest: %w; could not remove unregistered package at %s: %v", err, finalDir, removeErr)
		}
		return Manifest{}, previous, fmt.Errorf("could not register driver manifest: %w", err)
	}

	// Replacing a registration is already complete. Removing an old generation
	// is garbage collection, so its failure must not turn a successful install
	// or update into an error.
	_ = cleanup(loc, runtimeID, previous, finalDir, manifest.DriverInfo)
	return manifest, previous, nil
}

func normalizePackageInstallMetadata(runtimeID string, expected ExpectedPackageMetadata) (ExpectedPackageMetadata, error) {
	if err := validateFlatName(runtimeID); err != nil {
		return ExpectedPackageMetadata{}, fmt.Errorf("invalid runtime driver id: %w", err)
	}
	if expected.ID == "" {
		expected.ID = runtimeID
	}
	if expected.ID != runtimeID {
		return ExpectedPackageMetadata{}, fmt.Errorf("expected package id %q does not match runtime driver id %q", expected.ID, runtimeID)
	}
	if expected.ArchiveHash != "" || expected.ArchiveSize != 0 {
		if err := validateExpectedPackage(expected); err != nil {
			return ExpectedPackageMetadata{}, err
		}
	} else {
		if expected.Version != "" {
			if _, err := semver.NewVersion(expected.Version); err != nil {
				return ExpectedPackageMetadata{}, fmt.Errorf("invalid expected package version %q: %w", expected.Version, err)
			}
		}
		if expected.Platform != "" {
			if err := validatePlatformIdentifier(expected.Platform); err != nil {
				return ExpectedPackageMetadata{}, fmt.Errorf("invalid expected package platform: %w", err)
			}
		}
	}
	if err := validateExpectedPackageVersion(expected.PackageVersion); err != nil {
		return ExpectedPackageMetadata{}, err
	}
	if expected.SourceType == "" {
		expected.SourceType = "local"
	}
	if expected.SourceIdentity == "" {
		expected.SourceIdentity = "local"
	}
	return expected, nil
}

func loadInstalledDriver(cfg Config, loc, runtimeID string) (*DriverInfo, error) {
	var info DriverInfo
	var err error
	if cfg.Level == ConfigEnv {
		info, err = loadDriverFromManifest(loc, runtimeID)
	} else {
		info, err = GetDriver(cfg, runtimeID)
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &info, nil
}

func acquireDriverInstallLock(location, runtimeID string) (func(), error) {
	return acquireDriverInstallLockWith(location, runtimeID, acquirePackageInstallLock)
}

func acquireDriverInstallLockContext(ctx context.Context, location, runtimeID string) (func(), error) {
	return acquireDriverInstallLockWith(location, runtimeID, func(path string) (func(), error) {
		return acquirePackageInstallLockContext(ctx, path)
	})
}

func acquireDriverInstallLockWith(location, runtimeID string, acquire func(string) (func(), error)) (func(), error) {
	lockTarget := filepath.Join(location, runtimeID)
	lockHash := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(lockTarget))))
	return acquire(filepath.Join(location, ".dbc-package-install-"+hex.EncodeToString(lockHash[:])+".lock"))
}

func driverInstallLockLocation(cfg Config, info DriverInfo) (string, error) {
	location := info.FilePath
	registryPath := strings.HasPrefix(strings.ToUpper(location), "HKCU\\") || strings.HasPrefix(strings.ToUpper(location), "HKLM\\")
	if cfg.Level == ConfigEnv && location == "" {
		return "", errors.New("driver registration location is empty")
	}
	if cfg.Level != ConfigEnv && (registryPath || location == "") {
		location = cfg.Location
		if location == "" {
			location = cfg.Level.ConfigLocation()
		}
	}
	if location == "" {
		return "", errors.New("driver registration location is empty")
	}
	return filepath.Abs(location)
}

func uninstallPackageCleanupLocation(cfg Config, info DriverInfo) (string, error) {
	location := info.FilePath
	registryPath := strings.HasPrefix(strings.ToUpper(location), "HKCU\\") || strings.HasPrefix(strings.ToUpper(location), "HKLM\\")
	if registryPath || location == "" {
		location = cfg.Location
		if location == "" {
			location = cfg.Level.ConfigLocation()
		}
		if cfg.Level == ConfigEnv {
			paths := splitConfigList(location)
			if len(paths) == 0 {
				return "", errors.New("ADBC_DRIVER_PATH is empty, must be set to valid path to use")
			}
			location = paths[0]
		}
	}
	if location == "" {
		return "", errors.New("driver package cleanup location is empty")
	}
	return filepath.Abs(location)
}

func uninstallDriverWithInstallLock(cfg Config, info DriverInfo, uninstall func() error) error {
	location, err := driverInstallLockLocation(cfg, info)
	if err != nil {
		return fmt.Errorf("could not resolve driver registration location: %w", err)
	}
	releaseLock, err := acquireDriverInstallLock(location, info.ID)
	if err != nil {
		return fmt.Errorf("could not lock driver installation: %w", err)
	}
	defer releaseLock()

	var current DriverInfo
	if cfg.Level == ConfigEnv {
		current, err = loadDriverFromManifest(info.FilePath, info.ID)
	} else {
		current, err = GetDriver(cfg, info.ID)
	}
	if err != nil {
		return fmt.Errorf("driver registration changed before uninstall: could not reload current registration: %w", err)
	}
	if !sameDriverRegistration(info, current) {
		return errors.New("driver registration changed before uninstall; refusing to remove files from a stale registration")
	}
	return uninstall()
}

func sameDriverRegistration(first, second DriverInfo) bool {
	return normalizedRegistrationPath(first.FilePath) == normalizedRegistrationPath(second.FilePath) &&
		first.ID == second.ID && first.Name == second.Name && first.Publisher == second.Publisher &&
		first.License == second.License && first.Source == second.Source &&
		semverEquivalent(first.Version, second.Version) &&
		semverEquivalent(first.AdbcInfo.Version, second.AdbcInfo.Version) &&
		slices.Equal(normalizedFeatureSet(first.AdbcInfo.Features.Supported), normalizedFeatureSet(second.AdbcInfo.Features.Supported)) &&
		slices.Equal(normalizedFeatureSet(first.AdbcInfo.Features.Unsupported), normalizedFeatureSet(second.AdbcInfo.Features.Unsupported)) &&
		first.Driver.Entrypoint == second.Driver.Entrypoint &&
		first.Driver.Shared.defaultPath == second.Driver.Shared.defaultPath &&
		maps.Equal(first.Driver.Shared.platformMap, second.Driver.Shared.platformMap)
}

func runtimeRegistrationFingerprint(info DriverInfo, platform string, shared registrationSharedIdentity) (string, error) {
	if err := validatePlatformIdentifier(platform); err != nil {
		return "", fmt.Errorf("invalid registration fingerprint platform: %w", err)
	}
	if !registrationSharedIdentityValid(shared) {
		return "", errors.New("invalid registration fingerprint shared identity")
	}
	wire := runtimeRegistrationFingerprintV1{
		SchemaVersion: registrationFingerprintVersion,
		ID:            info.ID,
		Platform:      platform,
		Source:        info.Source,
		Name:          info.Name,
		Publisher:     info.Publisher,
		License:       info.License,
		Entrypoint:    info.Driver.Entrypoint,
		Version:       semverString(info.Version),
		ADBCVersion:   semverString(info.AdbcInfo.Version),
		Supported:     normalizedFeatureSet(info.AdbcInfo.Features.Supported),
		Unsupported:   normalizedFeatureSet(info.AdbcInfo.Features.Unsupported),
		Shared:        shared,
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return "", fmt.Errorf("could not encode canonical runtime registration: %w", err)
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func registrationSharedIdentityValid(identity registrationSharedIdentity) bool {
	switch identity.Kind {
	case "package_file":
		return validateFlatName(identity.PackageFile) == nil && identity.ExternalReference == ""
	case "external":
		return identity.PackageFile == ""
	default:
		return false
	}
}

func semverString(version *semver.Version) *string {
	if version == nil {
		return nil
	}
	value := version.String()
	return &value
}

func semverEquivalent(first, second *semver.Version) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.String() == second.String()
}

func normalizedFeatureSet(features []string) []string {
	normalized := slices.Clone(features)
	slices.Sort(normalized)
	normalized = slices.Compact(normalized)
	if normalized == nil {
		return []string{}
	}
	return normalized
}

func validRegistrationFingerprint(algorithm string, version int, fingerprint string) bool {
	if algorithm != registrationFingerprintAlgorithm || version != registrationFingerprintVersion {
		return false
	}
	_, err := parseSHA256(fingerprint)
	return err == nil
}

func sharedIdentityMatchesRegistration(identity registrationSharedIdentity, current DriverInfo, platform string) bool {
	sharedPath := current.Driver.Shared.Get(platform)
	switch identity.Kind {
	case "package_file":
		return validateFlatName(identity.PackageFile) == nil && sharedPath != "" && filepath.Base(filepath.Clean(sharedPath)) == identity.PackageFile
	case "external":
		if sharedPath == identity.ExternalReference {
			return true
		}
		return identity.ExternalReference == "" && sharedPath != "" && isGeneratedPackageDirectoryRegistration(current, sharedPath)
	default:
		return false
	}
}

func isGeneratedPackageDirectoryRegistration(current DriverInfo, sharedPath string) bool {
	if current.ID == "" || current.FilePath == "" {
		return false
	}
	sharedDirectory := filepath.Clean(sharedPath)
	registrationDirectory := filepath.Clean(current.FilePath)
	if filepath.Dir(sharedDirectory) != registrationDirectory {
		return false
	}
	return isManagedPackageGenerationDirectory(filepath.Base(sharedDirectory), current.ID)
}

func normalizedRegistrationPath(path string) string {
	if strings.HasPrefix(strings.ToUpper(path), "HKCU\\") || strings.HasPrefix(strings.ToUpper(path), "HKLM\\") {
		return strings.ToUpper(filepath.Clean(path))
	}
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func managedPackageDirectory(location, runtimeID string, info DriverInfo) (string, bool) {
	directories := managedPackageDirectories(location, runtimeID, info)
	if len(directories) == 0 {
		return "", false
	}
	return directories[0], true
}

func managedPackageDirectories(location, runtimeID string, info DriverInfo) []string {
	if info.Source != "dbc" || info.ID != runtimeID || info.Version == nil {
		return nil
	}
	directories := make([]string, 0, 1)
	seen := make(map[string]struct{})
	for sharedPath := range info.Driver.Shared.Paths() {
		if sharedPath == "" {
			continue
		}
		if !filepath.IsAbs(sharedPath) {
			sharedPath = filepath.Join(location, sharedPath)
		}
		dir, err := filepath.Abs(filepath.Dir(sharedPath))
		if err != nil {
			continue
		}
		receipt, ok := readPackageReceipt(location, runtimeID, dir)
		if !ok || receipt.InstalledLibrary == "" || receipt.DriverVersion != info.Version.String() {
			continue
		}
		registeredPath, found := info.Driver.Shared.platformMap[receipt.Platform]
		if info.Driver.Shared.defaultPath != "" {
			registeredPath, found = info.Driver.Shared.defaultPath, true
		}
		if !found {
			continue
		}
		if !filepath.IsAbs(registeredPath) {
			registeredPath = filepath.Join(location, registeredPath)
		}
		if filepath.Clean(registeredPath) != filepath.Clean(sharedPath) || filepath.Base(sharedPath) != receipt.InstalledLibrary {
			continue
		}
		dir = filepath.Clean(dir)
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		directories = append(directories, dir)
	}
	return directories
}

func cleanupManagedPackageDirectories(location, runtimeID, currentDir string, current DriverInfo) {
	_ = cleanupOwnedPackageDirectories(location, runtimeID, nil, currentDir, current)
}

func cleanupOwnedPackageDirectories(location, runtimeID string, previous *DriverInfo, currentDir string, current DriverInfo) error {
	return cleanupOwnedPackageDirectoriesWithRemoveAll(location, runtimeID, previous, currentDir, current, os.RemoveAll)
}

func cleanupOwnedPackageDirectoriesWithRemoveAll(location, runtimeID string, previous *DriverInfo, currentDir string, current DriverInfo, removeAll func(string) error) error {
	if validateFlatName(runtimeID) != nil {
		return nil
	}
	var cleanupErr error
	remove := func(directory string) {
		if err := removeAll(directory); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("could not remove owned package directory %s: %w", directory, err))
		}
	}
	candidates := make(map[string]struct{})
	if previous != nil {
		for _, directory := range managedPackageDirectories(location, runtimeID, *previous) {
			if !packageDirectoryIsCurrent(location, currentDir, current, directory) {
				directory = filepath.Clean(directory)
				candidates[directory] = struct{}{}
			}
		}
	}
	entries, err := readDirEntries(location)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("could not inspect package directory %s: %w", location, err))
		}
	} else {
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), ".dbc-package-"+runtimeID+"-") {
				continue
			}
			dir := filepath.Join(location, entry.Name())
			absDir, err := filepath.Abs(dir)
			if err != nil {
				continue
			}
			absDir = filepath.Clean(absDir)
			if packageDirectoryIsCurrent(location, currentDir, current, absDir) {
				continue
			}
			if _, ok := readManagedPackageReceipt(location, runtimeID, dir); !ok {
				continue
			}
			candidates[absDir] = struct{}{}
		}
	}
	for directory := range candidates {
		remove(directory)
	}
	cleanupErr = errors.Join(cleanupErr, cleanupLegacyPackageDirectoryWithRemoveAll(location, runtimeID, previous, current, removeAll))
	return cleanupErr
}

func packageDirectoryIsCurrent(location, currentDir string, current DriverInfo, directory string) bool {
	if currentDir != "" {
		absCurrent, currentErr := filepath.Abs(currentDir)
		absDirectory, directoryErr := filepath.Abs(directory)
		if currentErr == nil && directoryErr == nil && filepath.Clean(absCurrent) == filepath.Clean(absDirectory) {
			return true
		}
	}
	return runtimeReferencesPackageDirectory(current, location, directory)
}

func cleanupLegacyPackageDirectoryWithRemoveAll(location, runtimeID string, previous *DriverInfo, current DriverInfo, removeAll func(string) error) error {
	if previous == nil || previous.Source != "dbc" || previous.ID != runtimeID || previous.Version == nil {
		return nil
	}
	if validateFlatName(runtimeID) != nil {
		return nil
	}
	root, err := filepath.Abs(location)
	if err != nil {
		return nil
	}
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() {
		return nil
	}

	platformPaths := make(map[string]string)
	if previous.Driver.Shared.defaultPath != "" {
		platformPaths[PlatformTuple()] = previous.Driver.Shared.defaultPath
	} else {
		for platform, path := range previous.Driver.Shared.platformMap {
			platformPaths[platform] = path
		}
	}

	removed := make(map[string]struct{})
	var cleanupErr error
	for platform, sharedPath := range platformPaths {
		if sharedPath == "" || validatePlatformIdentifier(platform) != nil {
			continue
		}
		name := runtimeID + "_" + platform + "_v" + previous.Version.String()
		if validateFlatName(name) != nil {
			continue
		}
		candidate := filepath.Join(root, name)
		candidateAbs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, candidateAbs)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.Contains(rel, string(filepath.Separator)) {
			continue
		}
		if _, alreadyRemoved := removed[candidateAbs]; alreadyRemoved {
			continue
		}
		candidateInfo, err := os.Lstat(candidateAbs)
		if err != nil || !candidateInfo.IsDir() || candidateInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		// Any receipt means this directory follows the newer ownership protocol,
		// including malformed receipts that cannot be used to prove ownership.
		if _, err := os.Lstat(filepath.Join(candidateAbs, installReceiptName)); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if runtimeReferencesPackageDirectory(current, root, candidateAbs) {
			continue
		}
		if !legacySharedFileIsOwned(root, candidateAbs, sharedPath) {
			continue
		}
		if err := removeAll(candidateAbs); err == nil {
			removed[candidateAbs] = struct{}{}
		} else {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("could not remove owned legacy package directory %s: %w", candidateAbs, err))
		}
	}
	return cleanupErr
}

func legacySharedFileIsOwned(location, candidate, sharedPath string) bool {
	if !filepath.IsAbs(sharedPath) {
		sharedPath = filepath.Join(location, sharedPath)
	}
	sharedPath, err := filepath.Abs(sharedPath)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(candidate, sharedPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.Contains(rel, string(filepath.Separator)) {
		return false
	}
	info, err := os.Lstat(sharedPath)
	return err == nil && info.Mode().IsRegular()
}

func runtimeReferencesPackageDirectory(info DriverInfo, location, directory string) bool {
	for sharedPath := range info.Driver.Shared.Paths() {
		if sharedPath == "" {
			continue
		}
		if !filepath.IsAbs(sharedPath) {
			sharedPath = filepath.Join(location, sharedPath)
		}
		sharedPath = filepath.Clean(sharedPath)
		if sharedPath == filepath.Clean(directory) || filepath.Dir(sharedPath) == filepath.Clean(directory) {
			return true
		}
	}
	return false
}

func readManagedPackageReceipt(location, runtimeID, directory string) (InstallReceipt, bool) {
	if validateFlatName(runtimeID) != nil || !strings.HasPrefix(filepath.Base(filepath.Clean(directory)), ".dbc-package-"+runtimeID+"-") {
		return InstallReceipt{}, false
	}
	return readPackageReceipt(location, runtimeID, directory)
}

func readPackageReceipt(location, runtimeID, directory string) (InstallReceipt, bool) {
	receipt, ok := readPackageReceiptEvidence(location, runtimeID, directory)
	if !ok {
		return InstallReceipt{}, false
	}
	if receipt.InstalledLibrary == "" {
		return receipt, true
	}
	libraryPath := filepath.Join(directory, receipt.InstalledLibrary)
	if !VerifyInstallReceiptLibraryIntegrity(libraryPath, receipt) {
		return InstallReceipt{}, false
	}
	return receipt, true
}

// InspectInstallReceipt reads the receipt associated with a registered library
// path without checking the library bytes. The managed result identifies paths
// in a dbc-owned package generation even when its receipt is missing or invalid;
// present reports whether a receipt file exists, and valid reports whether its
// metadata and library/path relationship are structurally valid. Call
// VerifyInstallReceiptLibraryIntegrity separately to verify the current bytes.
func InspectInstallReceipt(location, runtimeID, libraryPath string) (receipt InstallReceipt, managed, present, valid bool) {
	if validateFlatName(runtimeID) != nil || libraryPath == "" {
		return InstallReceipt{}, false, false, false
	}
	absLocation, err := filepath.Abs(location)
	if err != nil {
		return InstallReceipt{}, false, false, false
	}
	absLibrary, err := filepath.Abs(libraryPath)
	if err != nil {
		return InstallReceipt{}, false, false, false
	}
	directory := filepath.Dir(absLibrary)
	rel, err := filepath.Rel(absLocation, directory)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.Contains(rel, string(filepath.Separator)) {
		return InstallReceipt{}, false, false, false
	}
	if !isManagedPackageGenerationDirectory(filepath.Base(directory), runtimeID) {
		return InstallReceipt{}, false, false, false
	}
	managed = true
	_, err = os.Lstat(filepath.Join(directory, installReceiptName))
	present = err == nil || !errors.Is(err, fs.ErrNotExist)
	if !present {
		return InstallReceipt{}, managed, false, false
	}
	receipt, valid = readPackageReceiptEvidence(absLocation, runtimeID, directory)
	if !valid {
		return InstallReceipt{}, managed, present, false
	}
	if receipt.InstalledLibrary == "" || filepath.Base(absLibrary) != receipt.InstalledLibrary {
		return InstallReceipt{}, managed, present, false
	}
	return receipt, managed, present, true
}

// InspectDriverInstallReceipt resolves the filesystem root for the driver's
// actual registration before inspecting its managed package receipt. This is
// important for ConfigEnv, where Config.Location may be a path list while the
// selected DriverInfo.FilePath names the directory that supplied the active
// manifest. Registry-backed Windows registrations fall back to the configured
// filesystem installation root.
func InspectDriverInstallReceipt(cfg Config, info DriverInfo) (receipt InstallReceipt, managed, present, valid bool, err error) {
	location, err := uninstallPackageCleanupLocation(cfg, info)
	if err != nil {
		return InstallReceipt{}, false, false, false, err
	}
	receipt, managed, present, valid = InspectInstallReceipt(location, info.ID, info.Driver.Shared.Get(PlatformTuple()))
	return receipt, managed, present, valid, nil
}

func isManagedPackageGenerationDirectory(directory, runtimeID string) bool {
	if strings.HasPrefix(directory, ".dbc-package-"+runtimeID+"-") {
		return true
	}
	legacyPrefix := runtimeID + "_"
	if !strings.HasPrefix(directory, legacyPrefix) {
		return false
	}
	remainder := strings.TrimPrefix(directory, legacyPrefix)
	versionSeparator := strings.LastIndex(remainder, "_v")
	if versionSeparator <= 0 {
		return false
	}
	platform, version := remainder[:versionSeparator], remainder[versionSeparator+2:]
	if validatePlatformIdentifier(platform) != nil {
		return false
	}
	_, err := semver.NewVersion(version)
	return err == nil
}

// VerifyInstallReceiptLibraryIntegrity checks only that the current registered
// library is a regular file whose digest matches the receipt. Receipt identity
// and artifact metadata are intentionally checked by the caller separately.
func VerifyInstallReceiptLibraryIntegrity(libraryPath string, receipt InstallReceipt) bool {
	if libraryPath == "" || validateFlatName(receipt.InstalledLibrary) != nil {
		return false
	}
	if filepath.Base(filepath.Clean(libraryPath)) != receipt.InstalledLibrary {
		return false
	}
	if _, err := parseSHA256(receipt.InstalledLibraryHash); err != nil {
		return false
	}
	libraryInfo, err := os.Lstat(libraryPath)
	if err != nil || !libraryInfo.Mode().IsRegular() {
		return false
	}
	actualHash, err := hashFile(libraryPath)
	return err == nil && actualHash == receipt.InstalledLibraryHash
}

func readPackageReceiptEvidence(location, runtimeID, directory string) (InstallReceipt, bool) {
	var receipt InstallReceipt
	absLocation, err := filepath.Abs(location)
	if err != nil {
		return receipt, false
	}
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return receipt, false
	}
	rel, err := filepath.Rel(absLocation, absDirectory)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.Contains(rel, string(filepath.Separator)) {
		return receipt, false
	}
	dirInfo, err := os.Lstat(absDirectory)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return receipt, false
	}
	receiptPath := filepath.Join(absDirectory, installReceiptName)
	receiptInfo, err := os.Lstat(receiptPath)
	if err != nil || !receiptInfo.Mode().IsRegular() {
		return receipt, false
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil || json.Unmarshal(data, &receipt) != nil {
		return InstallReceipt{}, false
	}
	if receipt.DriverID != runtimeID || validateFlatName(receipt.DriverID) != nil ||
		strings.TrimSpace(receipt.SourceType) == "" || strings.TrimSpace(receipt.SourceIdentity) == "" ||
		receipt.ArchiveSize <= 0 || parseReceiptMetadata(receipt) != nil {
		return InstallReceipt{}, false
	}
	hasLibrary := receipt.InstalledLibrary != "" || receipt.InstalledLibraryHash != ""
	if !hasLibrary {
		return receipt, true
	}
	if validateFlatName(receipt.InstalledLibrary) != nil {
		return InstallReceipt{}, false
	}
	if _, err := parseSHA256(receipt.InstalledLibraryHash); err != nil {
		return InstallReceipt{}, false
	}
	return receipt, true
}

func parseReceiptMetadata(receipt InstallReceipt) error {
	if err := validateExpectedPackageVersion(receipt.PackageVersion); err != nil {
		return fmt.Errorf("invalid receipt package version: %w", err)
	}
	if _, err := semver.NewVersion(receipt.DriverVersion); err != nil {
		return fmt.Errorf("invalid receipt driver version: %w", err)
	}
	if err := validatePlatformIdentifier(receipt.Platform); err != nil {
		return fmt.Errorf("invalid receipt platform: %w", err)
	}
	if _, err := parseSHA256(receipt.ArchiveHash); err != nil {
		return fmt.Errorf("invalid receipt archive hash: %w", err)
	}
	return nil
}

func validateExpectedPackage(expected ExpectedPackageMetadata) error {
	if err := validateFlatName(expected.ID); err != nil {
		return fmt.Errorf("invalid expected package id: %w", err)
	}
	for name, value := range map[string]string{
		"version":         expected.Version,
		"platform":        expected.Platform,
		"source type":     expected.SourceType,
		"source identity": expected.SourceIdentity,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("expected package %s is required", name)
		}
	}
	if _, err := semver.NewVersion(expected.Version); err != nil {
		return fmt.Errorf("invalid expected package version %q: %w", expected.Version, err)
	}
	if err := validateExpectedPackageVersion(expected.PackageVersion); err != nil {
		return err
	}
	if err := validatePlatformIdentifier(expected.Platform); err != nil {
		return fmt.Errorf("invalid expected package platform: %w", err)
	}
	if expected.ArchiveSize <= 0 {
		return errors.New("expected archive size must be positive")
	}
	if _, err := parseSHA256(expected.ArchiveHash); err != nil {
		return fmt.Errorf("invalid expected archive hash: %w", err)
	}
	return nil
}

func validateExpectedPackageVersion(version int) error {
	if version != 0 && version != 2 {
		return fmt.Errorf("unsupported dbc package version %d", version)
	}
	return nil
}

// InspectPackageMetadata decodes the archive's package metadata without
// installing files. It is intended to classify legacy and versioned package
// formats before choosing an install policy; the installer still validates
// the full archive before publication. The archive remains open for the caller.
func InspectPackageMetadata(downloaded *os.File) (Manifest, error) {
	if downloaded == nil {
		return Manifest{}, errors.New("package archive is nil")
	}
	workDir, err := os.MkdirTemp("", "dbc-package-inspect-")
	if err != nil {
		return Manifest{}, fmt.Errorf("could not create package inspection directory: %w", err)
	}
	defer os.RemoveAll(workDir)
	archivePath := filepath.Join(workDir, "archive.tgz")
	if _, _, err := snapshotArchive(downloaded, archivePath); err != nil {
		return Manifest{}, fmt.Errorf("could not snapshot package archive: %w", err)
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		return Manifest{}, err
	}
	defer archive.Close()
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not create gzip reader: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	seen := make(map[string]string)
	metadata := make(map[string][]byte)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("error reading package archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			if header.Typeflag == tar.TypeDir {
				return Manifest{}, fmt.Errorf("found a directory entry %q; driver archives must be flat", header.Name)
			}
			return Manifest{}, fmt.Errorf("archive entry %q is not a regular file", header.Name)
		}
		if err := validateFlatName(header.Name); err != nil {
			return Manifest{}, fmt.Errorf("invalid archive entry %q: %w", header.Name, err)
		}
		folded := strings.ToLower(header.Name)
		if previous, ok := seen[folded]; ok {
			return Manifest{}, fmt.Errorf("archive entries %q and %q collide by name", previous, header.Name)
		}
		seen[folded] = header.Name
		metadataName, isMetadata, err := classifyPackageMetadataName(header.Name)
		if err != nil {
			return Manifest{}, err
		}
		if !isMetadata {
			if _, err := io.Copy(io.Discard, reader); err != nil {
				return Manifest{}, fmt.Errorf("could not skip package file %q: %w", header.Name, err)
			}
			continue
		}
		if header.Size < 0 || header.Size > maxPackageMetadataSize {
			return Manifest{}, errors.New("package metadata size is invalid or exceeds 1 MiB")
		}
		data, err := io.ReadAll(io.LimitReader(reader, maxPackageMetadataSize+1))
		if err != nil {
			return Manifest{}, fmt.Errorf("could not read package metadata: %w", err)
		}
		if int64(len(data)) != header.Size {
			return Manifest{}, errors.New("package metadata size does not match its tar header")
		}
		metadata[metadataName] = data
	}
	metadataName, data, err := selectPackageMetadata(metadata)
	if err != nil {
		return Manifest{}, err
	}
	manifest, err := decodePackageManifest(metadataName, data)
	if err != nil {
		return Manifest{}, err
	}
	return manifest.manifest, nil
}

func installPackageArchive(cfg Config, targetName, runtimeID string, downloaded *os.File, expected ExpectedPackageMetadata) (Manifest, error) {
	var result Manifest
	if downloaded == nil {
		return result, errors.New("package archive is nil")
	}
	if err := validateFlatName(targetName); err != nil {
		return result, fmt.Errorf("invalid installation directory name: %w", err)
	}
	if err := validateFlatName(runtimeID); err != nil {
		return result, fmt.Errorf("invalid runtime driver id: %w", err)
	}
	loc, err := EnsureLocation(cfg)
	if err != nil {
		return result, fmt.Errorf("could not ensure config location: %w", err)
	}
	finalDir := filepath.Join(loc, targetName)
	lockTarget, err := filepath.Abs(finalDir)
	if err != nil {
		return result, fmt.Errorf("could not resolve installation target: %w", err)
	}
	lockHash := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(lockTarget))))
	lockPath := filepath.Join(loc, ".dbc-package-install-"+hex.EncodeToString(lockHash[:])+".lock")
	releaseLock, err := acquirePackageInstallLock(lockPath)
	if err != nil {
		return result, fmt.Errorf("could not lock package installation target: %w", err)
	}
	defer releaseLock()
	workDir, err := os.MkdirTemp(loc, ".dbc-install-")
	if err != nil {
		return result, fmt.Errorf("could not create private installation staging directory: %w", err)
	}
	defer os.RemoveAll(workDir)
	manifest, payloadDir, _, err := stagePackageArchive(loc, runtimeID, finalDir, downloaded, expected, nil, workDir)
	if err != nil {
		return result, err
	}
	if err := publishDirectory(payloadDir, finalDir); err != nil {
		return result, fmt.Errorf("could not publish package directory: %w", err)
	}
	return manifest, nil
}

func stagePackageArchive(location, runtimeID, finalDir string, downloaded *os.File, expected ExpectedPackageMetadata, verify func(string, Manifest) error, workDir string) (Manifest, string, registrationSharedIdentity, error) {
	var result Manifest
	var sharedIdentity registrationSharedIdentity
	archivePath := filepath.Join(workDir, "archive.tgz")
	archiveHash, archiveSize, err := snapshotArchive(downloaded, archivePath)
	if err != nil {
		return result, "", sharedIdentity, fmt.Errorf("could not snapshot package archive: %w", err)
	}
	if expected.ArchiveHash != "" && expected.ArchiveHash != archiveHash {
		return result, "", sharedIdentity, fmt.Errorf("package archive hash mismatch: got %s, expected %s", archiveHash, expected.ArchiveHash)
	}
	if expected.ArchiveSize > 0 && expected.ArchiveSize != archiveSize {
		return result, "", sharedIdentity, fmt.Errorf("package archive size mismatch: got %d, expected %d", archiveSize, expected.ArchiveSize)
	}

	payloadDir := filepath.Join(workDir, "payload")
	if err := os.Mkdir(payloadDir, 0o700); err != nil {
		return result, "", sharedIdentity, fmt.Errorf("could not create private package staging directory: %w", err)
	}
	manifest, meta, files, err := extractPackageArchive(archivePath, payloadDir)
	if err != nil {
		return result, "", sharedIdentity, fmt.Errorf("failed to extract package archive: %w", err)
	}
	if expected.ID != "" && meta.v2 && expected.ID != meta.id {
		return result, "", sharedIdentity, fmt.Errorf("package id mismatch: archive declares %q, expected %q", meta.id, expected.ID)
	}
	if expected.Version != "" && manifest.Version.String() != expected.Version {
		return result, "", sharedIdentity, fmt.Errorf("package version mismatch: archive declares %q, expected %q", manifest.Version, expected.Version)
	}
	if expected.PackageVersion != 0 && expected.PackageVersion != manifest.PackageVersion {
		return result, "", sharedIdentity, fmt.Errorf("dbc package version mismatch: archive declares %d, expected %d", manifest.PackageVersion, expected.PackageVersion)
	}
	platform := expected.Platform
	if meta.v2 {
		if platform != "" && meta.platform != platform {
			return result, "", sharedIdentity, fmt.Errorf("package platform mismatch: archive declares %q, expected %q", meta.platform, platform)
		}
		platform = meta.platform
	}
	if platform == "" {
		platform = PlatformTuple()
	}
	if err := validatePackageFileReferences(manifest, files, meta.v2); err != nil {
		return result, "", sharedIdentity, err
	}
	if _, exists := files[strings.ToLower(installReceiptName)]; exists {
		return result, "", sharedIdentity, fmt.Errorf("package archive uses reserved file name %q", installReceiptName)
	}
	if manifest.Files.Driver != "" {
		if err := validateFlatName(manifest.Files.Driver); err != nil {
			return result, "", sharedIdentity, fmt.Errorf("invalid package driver file name: %w", err)
		}
		sharedIdentity = registrationSharedIdentity{Kind: "package_file", PackageFile: manifest.Files.Driver}
	} else {
		sharedIdentity = registrationSharedIdentity{Kind: "external", ExternalReference: manifest.DriverInfo.Driver.Shared.Get(platform)}
	}

	manifest.DriverInfo.ID = runtimeID
	manifest.DriverInfo.Source = "dbc"
	installedLibrary := ""
	if manifest.Files.Driver != "" {
		manifest.DriverInfo.Driver.Shared.Set(platform, filepath.Join(finalDir, manifest.Files.Driver))
		installedLibrary = manifest.Files.Driver
	} else if !hasRuntimeSharedPath(manifest.DriverInfo.Driver.Shared) {
		manifest.DriverInfo.Driver.Shared.Set(platform, finalDir)
	}
	if err := os.Chmod(payloadDir, 0o755); err != nil {
		return result, "", sharedIdentity, fmt.Errorf("could not prepare package directory for publication: %w", err)
	}
	if verify != nil {
		if err := verify(payloadDir, manifest); err != nil {
			return result, "", sharedIdentity, fmt.Errorf("package verification failed: %w", err)
		}
	}
	installedHash := ""
	if installedLibrary != "" {
		libraryPath := filepath.Join(payloadDir, installedLibrary)
		libraryInfo, err := os.Lstat(libraryPath)
		if err != nil {
			return result, "", sharedIdentity, fmt.Errorf("could not inspect verified driver file: %w", err)
		}
		if !libraryInfo.Mode().IsRegular() {
			return result, "", sharedIdentity, errors.New("verified driver file is not a regular file")
		}
		installedHash, err = hashFile(libraryPath)
		if err != nil {
			return result, "", sharedIdentity, fmt.Errorf("could not hash verified driver file: %w", err)
		}
	}
	registrationFingerprint, err := runtimeRegistrationFingerprint(manifest.DriverInfo, platform, sharedIdentity)
	if err != nil {
		return result, "", sharedIdentity, fmt.Errorf("could not fingerprint package registration: %w", err)
	}
	receipt := InstallReceipt{
		SourceType: expected.SourceType, SourceIdentity: expected.SourceIdentity,
		DriverID: runtimeID, DriverVersion: manifest.Version.String(), Platform: platform,
		PackageVersion: manifest.PackageVersion,
		ArchiveHash:    archiveHash, ArchiveSize: archiveSize, InstalledLibrary: installedLibrary,
		InstalledLibraryHash:             installedHash,
		RegistrationFingerprintAlgorithm: registrationFingerprintAlgorithm,
		RegistrationFingerprintVersion:   registrationFingerprintVersion,
		RegistrationFingerprint:          registrationFingerprint,
	}
	if err := writeInstallReceipt(payloadDir, receipt); err != nil {
		return result, "", sharedIdentity, fmt.Errorf("could not write installation receipt: %w", err)
	}
	return manifest, payloadDir, sharedIdentity, nil
}

func hasRuntimeSharedPath(shared driverMap) bool {
	if shared.defaultPath != "" {
		return true
	}
	for _, path := range shared.platformMap {
		if path != "" {
			return true
		}
	}
	return false
}

func snapshotArchive(source *os.File, target string) (string, int64, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return "", 0, fmt.Errorf("could not seek to archive start: %w", err)
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(output, h), source)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		return "", n, copyErr
	}
	if syncErr != nil {
		return "", n, syncErr
	}
	if closeErr != nil {
		return "", n, closeErr
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, nil
}

func extractPackageArchive(archivePath, payloadDir string) (Manifest, packageManifest, map[string]string, error) {
	var empty Manifest
	archive, err := os.Open(archivePath)
	if err != nil {
		return empty, packageManifest{}, nil, err
	}
	defer archive.Close()
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return empty, packageManifest{}, nil, fmt.Errorf("could not create gzip reader: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	seen := make(map[string]string)
	metadata := make(map[string][]byte)
	files := make(map[string]string)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return empty, packageManifest{}, nil, fmt.Errorf("error reading tar archive: %w", err)
		}
		if header.Typeflag == tar.TypeDir {
			return empty, packageManifest{}, nil, fmt.Errorf("found a directory entry %q; driver archives must be flat", header.Name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return empty, packageManifest{}, nil, fmt.Errorf("archive entry %q is not a regular file", header.Name)
		}
		for key := range header.PAXRecords {
			if strings.HasPrefix(key, "GNU.sparse") {
				return empty, packageManifest{}, nil, fmt.Errorf("archive entry %q uses an unsupported sparse-file extension", header.Name)
			}
		}
		if err := validateFlatName(header.Name); err != nil {
			return empty, packageManifest{}, nil, fmt.Errorf("invalid archive entry %q: %w", header.Name, err)
		}
		if header.Size < 0 {
			return empty, packageManifest{}, nil, fmt.Errorf("archive entry %q has a negative size", header.Name)
		}
		folded := strings.ToLower(header.Name)
		if previous, ok := seen[folded]; ok {
			return empty, packageManifest{}, nil, fmt.Errorf("archive entries %q and %q collide by name", previous, header.Name)
		}
		seen[folded] = header.Name
		metadataName, isMetadata, err := classifyPackageMetadataName(header.Name)
		if err != nil {
			return empty, packageManifest{}, nil, err
		}
		if isMetadata {
			if header.Size > maxPackageMetadataSize {
				return empty, packageManifest{}, nil, errors.New("package metadata exceeds 1 MiB")
			}
			data, err := io.ReadAll(io.LimitReader(reader, maxPackageMetadataSize+1))
			if err != nil {
				return empty, packageManifest{}, nil, fmt.Errorf("could not read package metadata: %w", err)
			}
			if int64(len(data)) != header.Size {
				return empty, packageManifest{}, nil, errors.New("package metadata size does not match its tar header")
			}
			metadata[metadataName] = data
			continue
		}
		filePath := filepath.Join(payloadDir, header.Name)
		mode := os.FileMode(header.Mode & 0o777)
		if mode == 0 {
			mode = 0o644
		}
		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return empty, packageManifest{}, nil, fmt.Errorf("could not create staged package file %q: %w", header.Name, err)
		}
		written, writeErr := io.Copy(file, reader)
		if writeErr == nil && written != header.Size {
			writeErr = fmt.Errorf("archive entry %q size mismatch: read %d, header says %d", header.Name, written, header.Size)
		}
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil {
			return empty, packageManifest{}, nil, fmt.Errorf("could not write staged package file %q: %w", header.Name, writeErr)
		}
		if closeErr != nil {
			return empty, packageManifest{}, nil, fmt.Errorf("could not close staged package file %q: %w", header.Name, closeErr)
		}
		files[folded] = header.Name
	}
	metadataName, data, err := selectPackageMetadata(metadata)
	if err != nil {
		return empty, packageManifest{}, nil, err
	}
	parsed, err := decodePackageManifest(metadataName, data)
	if err != nil {
		return empty, packageManifest{}, nil, err
	}
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return empty, packageManifest{}, nil, fmt.Errorf("could not verify gzip stream: %w", err)
	}
	if err := validatePackageFileReferences(parsed.manifest, files, parsed.v2); err != nil {
		return empty, packageManifest{}, nil, err
	}
	return parsed.manifest, parsed, files, nil
}

func validatePackageFileReferences(manifest Manifest, files map[string]string, requireDriver bool) error {
	driver := manifest.Files.Driver
	if driver == "" {
		if requireDriver {
			// TODO: Model externally managed drivers as an explicit package variant with ownership semantics, not an empty Files.driver exception.
			return fmt.Errorf("%w: Files.driver is required", ErrInvalidManifest)
		}
	} else {
		if err := validateFlatName(driver); err != nil {
			return fmt.Errorf("%w: invalid Files.driver: %v", ErrInvalidManifest, err)
		}
		if archived, exists := files[strings.ToLower(driver)]; !exists || archived != driver {
			return fmt.Errorf("%w: driver file %q is missing from archive", ErrInvalidManifest, driver)
		}
	}
	if signature := manifest.Files.Signature; signature != "" {
		if err := validateFlatName(signature); err != nil {
			return fmt.Errorf("%w: invalid Files.signature: %v", ErrInvalidManifest, err)
		}
		if archived, exists := files[strings.ToLower(signature)]; !exists || archived != signature {
			return fmt.Errorf("%w: signature file %q is missing from archive", ErrInvalidManifest, signature)
		}
	}
	return nil
}

func validateFlatName(name string) error {
	if name == "" || name == "." || name == ".." {
		return errors.New("name must be a non-empty flat file name")
	}
	if strings.ContainsAny(name, "/\\\x00:") || filepath.IsAbs(name) {
		return errors.New("absolute paths and path separators are not allowed")
	}
	if strings.TrimSpace(name) != name || strings.HasSuffix(name, ".") {
		return errors.New("trailing spaces and dots are not allowed")
	}
	for _, char := range name {
		if char < 0x20 || strings.ContainsRune(`<>"|?*`, char) {
			return errors.New("name contains a character unsupported by Windows")
		}
	}
	deviceName := strings.ToUpper(strings.TrimRight(strings.SplitN(name, ".", 2)[0], " ."))
	switch deviceName {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return errors.New("name is reserved by Windows")
	}
	for _, prefix := range []string{"COM", "LPT"} {
		for _, suffix := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³"} {
			if strings.EqualFold(deviceName, prefix+suffix) {
				return errors.New("name is reserved by Windows")
			}
		}
	}
	return nil
}

func validatePlatformIdentifier(platform string) error {
	if platform == "" || strings.TrimSpace(platform) != platform {
		return errors.New("platform is required and must not have surrounding whitespace")
	}
	for _, char := range platform {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return errors.New("platform may contain only letters, digits, dot, underscore, and hyphen")
	}
	return nil
}

func parseSHA256(value string) ([]byte, error) {
	if !strings.HasPrefix(value, "sha256:") {
		return nil, errors.New("hash must use sha256:<lowercase hex>")
	}
	hexValue := strings.TrimPrefix(value, "sha256:")
	if len(hexValue) != sha256.Size*2 || strings.ToLower(hexValue) != hexValue {
		return nil, errors.New("sha256 digest must be 64 lowercase hexadecimal characters")
	}
	decoded, err := hex.DecodeString(hexValue)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func hashFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func writeInstallReceipt(directory string, receipt InstallReceipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(directory, ".receipt-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, filepath.Join(directory, installReceiptName))
}

func publishDirectory(staged, target string) error {
	// This protects the package directory against ordinary rename failures. It
	// does not make publication of this directory and the caller's separate
	// runtime manifest update one transaction.
	parent := filepath.Dir(target)
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return os.Rename(staged, target)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("existing installation target %q is not a directory", target)
	}
	backup, err := os.MkdirTemp(parent, ".dbc-backup-")
	if err != nil {
		return fmt.Errorf("could not reserve installation backup path: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("could not prepare installation backup path: %w", err)
	}
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("could not preserve existing installation: %w", err)
	}
	if err := os.Rename(staged, target); err != nil {
		restoreErr := os.Rename(backup, target)
		if restoreErr != nil {
			return fmt.Errorf("could not publish new installation: %w; restoring previous installation also failed: %v (preserved at %s)", err, restoreErr, backup)
		}
		return fmt.Errorf("could not publish new installation: %w", err)
	}
	// The new package is complete and visible. A leftover backup is safe if the
	// filesystem refuses cleanup, so cleanup failure does not invalidate install.
	_ = os.RemoveAll(backup)
	return nil
}

func publishExtractedFiles(staged string, outDir string, names []string) error {
	created := make([]string, 0, len(names))
	rollback := func() {
		for _, name := range created {
			_ = os.Remove(filepath.Join(outDir, name))
		}
	}
	for _, name := range names {
		destination := filepath.Join(outDir, name)
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			rollback()
			return fmt.Errorf("could not create extracted file %q: %w", name, err)
		}
		created = append(created, name)
		source, err := os.Open(filepath.Join(staged, name))
		if err != nil {
			_ = file.Close()
			rollback()
			return fmt.Errorf("could not open staged file %q: %w", name, err)
		}
		_, copyErr := io.Copy(file, source)
		stat, statErr := source.Stat()
		closeSourceErr := source.Close()
		closeDestErr := file.Close()
		if copyErr == nil && statErr == nil {
			copyErr = os.Chmod(destination, stat.Mode().Perm())
		}
		if copyErr != nil || statErr != nil || closeSourceErr != nil || closeDestErr != nil {
			rollback()
			return fmt.Errorf("could not publish extracted file %q: %w", name, errors.Join(copyErr, statErr, closeSourceErr, closeDestErr))
		}
	}
	return nil
}
