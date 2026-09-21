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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/pelletier/go-toml/v2"
)

const (
	installReceiptName        = "dbc-install-receipt.json"
	legacyPackageManifestName = "MANIFEST"
	packageV2MetadataName     = "dbc-package.toml"
	maxPackageMetadataSize    = 1 << 20
)

// ExpectedPackageMetadata describes the resolution that selected an archive.
// Hashes use the canonical form "sha256:<lowercase hex>". ArchiveSize is the
// size of the compressed archive, not the extracted library.
type ExpectedPackageMetadata struct {
	ID             string
	Version        string
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
	SourceType           string `json:"source_type"`
	SourceIdentity       string `json:"source_identity"`
	DriverID             string `json:"driver_id"`
	DriverVersion        string `json:"driver_version"`
	Platform             string `json:"platform"`
	ArchiveHash          string `json:"archive_hash"`
	ArchiveSize          int64  `json:"archive_size"`
	InstalledLibrary     string `json:"installed_library,omitempty"`
	InstalledLibraryHash string `json:"installed_library_hash,omitempty"`
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
	VerifiedLibraryHash string
}

type packageManifest struct {
	manifest Manifest
	id       string
	platform string
	v2       bool
}

type packageManifestV2Wire struct {
	PackageVersion  int64           `toml:"package_version"`
	ManifestVersion *int64          `toml:"manifest_version"`
	ID              string          `toml:"id"`
	Name            string          `toml:"name"`
	Version         *semver.Version `toml:"version"`
	Platform        string          `toml:"platform"`
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
	if wire.Version == nil {
		return packageManifest{}, fmt.Errorf("%w: version is required", ErrInvalidManifest)
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
			PackageVersion: 2,
			DriverInfo: DriverInfo{
				ID:      wire.ID,
				Name:    wire.Name,
				Version: wire.Version,
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

// InstallPackageArchive verifies and installs an already-downloaded package.
// The expected metadata must describe the resolution that selected the
// archive. The archive remains open for the caller.
func InstallPackageArchive(cfg Config, downloaded *os.File, expected ExpectedPackageMetadata) (Manifest, error) {
	if downloaded == nil {
		return Manifest{}, errors.New("package archive is nil")
	}
	if err := validateExpectedPackage(expected); err != nil {
		return Manifest{}, err
	}
	return installPackageArchive(cfg, expected.ID, expected.ID, downloaded, expected)
}

// InstallPackage prepares a package in a private generation directory, verifies
// it, registers its runtime manifest, and then removes a previous managed
// generation when its receipt proves ownership. The downloaded archive remains
// open for the caller.
func InstallPackage(cfg Config, runtimeID string, downloaded *os.File, expected ExpectedPackageMetadata, options InstallOptions) (Manifest, error) {
	return installPackage(cfg, runtimeID, downloaded, expected, options, CreateManifest)
}

// ValidatePackage verifies and stages an already-downloaded package in a
// private temporary directory without registering it or changing shared
// configuration. The temporary directory is removed before ValidatePackage
// returns. The archive remains open and can be passed to InstallPackage after
// validation.
func ValidatePackage(runtimeID string, downloaded *os.File, expected ExpectedPackageMetadata, options InstallOptions) (validation PackageValidation, err error) {
	if downloaded == nil {
		return PackageValidation{}, errors.New("package archive is nil")
	}
	expected, err = normalizePackageInstallMetadata(runtimeID, expected)
	if err != nil {
		return PackageValidation{}, err
	}
	workDir, err := os.MkdirTemp("", "dbc-package-validate-")
	if err != nil {
		return PackageValidation{}, fmt.Errorf("could not create private package validation directory: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(workDir); cleanupErr != nil {
			validation = PackageValidation{}
			err = errors.Join(err, fmt.Errorf("could not remove package validation directory: %w", cleanupErr))
		}
	}()

	finalDir := filepath.Join(workDir, "installed")
	_, payloadDir, err := stagePackageArchive(workDir, runtimeID, finalDir, downloaded, expected, options.Verify, workDir)
	if err != nil {
		return PackageValidation{}, err
	}
	receipt, ok := readPackageReceipt(workDir, runtimeID, payloadDir)
	if !ok {
		return PackageValidation{}, errors.New("could not read validated package receipt")
	}
	return PackageValidation{VerifiedLibraryHash: receipt.InstalledLibraryHash}, nil
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

	previous, err := loadInstalledDriver(cfg, loc, runtimeID)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not inspect existing driver registration: %w", err)
	}
	workDir, err := os.MkdirTemp(loc, ".dbc-install-")
	if err != nil {
		return Manifest{}, fmt.Errorf("could not create private installation staging directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	reservedDir, err := os.MkdirTemp(loc, ".dbc-package-"+runtimeID+"-")
	if err != nil {
		return Manifest{}, fmt.Errorf("could not reserve package generation directory: %w", err)
	}
	if err := os.Remove(reservedDir); err != nil {
		return Manifest{}, fmt.Errorf("could not prepare package generation directory: %w", err)
	}
	finalDir := reservedDir

	manifest, payloadDir, err := stagePackageArchive(loc, runtimeID, finalDir, downloaded, expected, options.Verify, workDir)
	if err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(payloadDir, finalDir); err != nil {
		return Manifest{}, fmt.Errorf("could not publish verified package generation: %w", err)
	}
	if err := registerManifest(cfg, manifest.DriverInfo); err != nil {
		var rollbackErr *manifestRollbackError
		if errors.As(err, &rollbackErr) {
			return Manifest{}, fmt.Errorf("could not register driver manifest; preserving verified package at %s: %w", finalDir, err)
		}
		if removeErr := os.RemoveAll(finalDir); removeErr != nil {
			return Manifest{}, fmt.Errorf("could not register driver manifest: %w; could not remove unregistered package at %s: %v", err, finalDir, removeErr)
		}
		return Manifest{}, fmt.Errorf("could not register driver manifest: %w", err)
	}

	// Replacing a registration is already complete. Removing an old generation
	// is garbage collection, so its failure must not turn a successful install
	// or update into an error.
	_ = cleanup(loc, runtimeID, previous, finalDir, manifest.DriverInfo)
	return manifest, nil
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
	lockTarget := filepath.Join(location, runtimeID)
	lockHash := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(lockTarget))))
	return acquirePackageInstallLock(filepath.Join(location, ".dbc-package-install-"+hex.EncodeToString(lockHash[:])+".lock"))
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
	first.FilePath = normalizedRegistrationPath(first.FilePath)
	second.FilePath = normalizedRegistrationPath(second.FilePath)
	return reflect.DeepEqual(first, second)
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
	entries, err := os.ReadDir(location)
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
	manifest, payloadDir, err := stagePackageArchive(loc, runtimeID, finalDir, downloaded, expected, nil, workDir)
	if err != nil {
		return result, err
	}
	if err := publishDirectory(payloadDir, finalDir); err != nil {
		return result, fmt.Errorf("could not publish package directory: %w", err)
	}
	return manifest, nil
}

func stagePackageArchive(location, runtimeID, finalDir string, downloaded *os.File, expected ExpectedPackageMetadata, verify func(string, Manifest) error, workDir string) (Manifest, string, error) {
	var result Manifest
	archivePath := filepath.Join(workDir, "archive.tgz")
	archiveHash, archiveSize, err := snapshotArchive(downloaded, archivePath)
	if err != nil {
		return result, "", fmt.Errorf("could not snapshot package archive: %w", err)
	}
	if expected.ArchiveHash != "" && expected.ArchiveHash != archiveHash {
		return result, "", fmt.Errorf("package archive hash mismatch: got %s, expected %s", archiveHash, expected.ArchiveHash)
	}
	if expected.ArchiveSize > 0 && expected.ArchiveSize != archiveSize {
		return result, "", fmt.Errorf("package archive size mismatch: got %d, expected %d", archiveSize, expected.ArchiveSize)
	}

	payloadDir := filepath.Join(workDir, "payload")
	if err := os.Mkdir(payloadDir, 0o700); err != nil {
		return result, "", fmt.Errorf("could not create private package staging directory: %w", err)
	}
	manifest, meta, files, err := extractPackageArchive(archivePath, payloadDir)
	if err != nil {
		return result, "", fmt.Errorf("failed to extract package archive: %w", err)
	}
	if expected.ID != "" && meta.v2 && expected.ID != meta.id {
		return result, "", fmt.Errorf("package id mismatch: archive declares %q, expected %q", meta.id, expected.ID)
	}
	if expected.Version != "" && manifest.Version.String() != expected.Version {
		return result, "", fmt.Errorf("package version mismatch: archive declares %q, expected %q", manifest.Version, expected.Version)
	}
	platform := expected.Platform
	if meta.v2 {
		if platform != "" && meta.platform != platform {
			return result, "", fmt.Errorf("package platform mismatch: archive declares %q, expected %q", meta.platform, platform)
		}
		platform = meta.platform
	}
	if platform == "" {
		platform = PlatformTuple()
	}
	if err := validatePackageFileReferences(manifest, files, meta.v2); err != nil {
		return result, "", err
	}
	if _, exists := files[strings.ToLower(installReceiptName)]; exists {
		return result, "", fmt.Errorf("package archive uses reserved file name %q", installReceiptName)
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
		return result, "", fmt.Errorf("could not prepare package directory for publication: %w", err)
	}
	if verify != nil {
		if err := verify(payloadDir, manifest); err != nil {
			return result, "", fmt.Errorf("package verification failed: %w", err)
		}
	}
	installedHash := ""
	if installedLibrary != "" {
		libraryPath := filepath.Join(payloadDir, installedLibrary)
		libraryInfo, err := os.Lstat(libraryPath)
		if err != nil {
			return result, "", fmt.Errorf("could not inspect verified driver file: %w", err)
		}
		if !libraryInfo.Mode().IsRegular() {
			return result, "", errors.New("verified driver file is not a regular file")
		}
		installedHash, err = hashFile(libraryPath)
		if err != nil {
			return result, "", fmt.Errorf("could not hash verified driver file: %w", err)
		}
	}
	receipt := InstallReceipt{
		SourceType: expected.SourceType, SourceIdentity: expected.SourceIdentity,
		DriverID: runtimeID, DriverVersion: manifest.Version.String(), Platform: platform,
		ArchiveHash: archiveHash, ArchiveSize: archiveSize, InstalledLibrary: installedLibrary,
		InstalledLibraryHash: installedHash,
	}
	if err := writeInstallReceipt(payloadDir, receipt); err != nil {
		return result, "", fmt.Errorf("could not write installation receipt: %w", err)
	}
	return manifest, payloadDir, nil
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
