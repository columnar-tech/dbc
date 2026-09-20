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
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/pelletier/go-toml/v2"
)

const installReceiptName = "dbc-install-receipt.json"

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
	InstalledLibraryHash string `json:"installed_library_hash,omitempty"`
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

func decodePackageManifest(data []byte) (packageManifest, error) {
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		return packageManifest{}, fmt.Errorf("%w: error decoding package manifest: %v", ErrInvalidManifest, err)
	}

	if rawVersion, hasDiscriminator := root["package_version"]; hasDiscriminator {
		version, ok := rawVersion.(int64)
		if !ok {
			return packageManifest{}, fmt.Errorf("%w: package_version must be an integer", ErrInvalidManifest)
		}
		if version != 2 {
			return packageManifest{}, fmt.Errorf("%w: package version %d is unsupported", ErrInvalidManifest, version)
		}

		var wire packageManifestV2Wire
		if err := toml.Unmarshal(data, &wire); err != nil {
			return packageManifest{}, fmt.Errorf("%w: error decoding package v2 manifest: %v", ErrInvalidManifest, err)
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

// InspectPackageManifest decodes the archive's MANIFEST without installing
// files. It is intended to classify legacy and versioned package wire formats
// before choosing an install policy; the installer still validates the full
// archive before publication. The archive remains open for the caller.
func InspectPackageManifest(downloaded *os.File) (Manifest, error) {
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
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return Manifest{}, errors.New("package archive has no MANIFEST")
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
		if strings.EqualFold(header.Name, "MANIFEST") && header.Name != "MANIFEST" {
			return Manifest{}, errors.New("package manifest must be named exactly MANIFEST")
		}
		if header.Name != "MANIFEST" {
			if _, err := io.Copy(io.Discard, reader); err != nil {
				return Manifest{}, fmt.Errorf("could not skip package file %q: %w", header.Name, err)
			}
			continue
		}
		if header.Size < 0 || header.Size > 1<<20 {
			return Manifest{}, errors.New("package manifest size is invalid or exceeds 1 MiB")
		}
		data, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
		if err != nil {
			return Manifest{}, fmt.Errorf("could not read package manifest: %w", err)
		}
		if int64(len(data)) != header.Size {
			return Manifest{}, errors.New("package manifest size does not match its tar header")
		}
		manifest, err := decodePackageManifest(data)
		if err != nil {
			return Manifest{}, err
		}
		return manifest.manifest, nil
	}
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

	archivePath := filepath.Join(workDir, "archive.tgz")
	archiveHash, archiveSize, err := snapshotArchive(downloaded, archivePath)
	if err != nil {
		return result, fmt.Errorf("could not snapshot package archive: %w", err)
	}
	if expected.ArchiveHash != "" && expected.ArchiveHash != archiveHash {
		return result, fmt.Errorf("package archive hash mismatch: got %s, expected %s", archiveHash, expected.ArchiveHash)
	}
	if expected.ArchiveSize > 0 && expected.ArchiveSize != archiveSize {
		return result, fmt.Errorf("package archive size mismatch: got %d, expected %d", archiveSize, expected.ArchiveSize)
	}

	payloadDir := filepath.Join(workDir, "payload")
	if err := os.Mkdir(payloadDir, 0o700); err != nil {
		return result, fmt.Errorf("could not create private package staging directory: %w", err)
	}
	manifest, meta, files, err := extractPackageArchive(archivePath, payloadDir)
	if err != nil {
		return result, fmt.Errorf("failed to extract package archive: %w", err)
	}
	if expected.ID != "" && meta.v2 && expected.ID != meta.id {
		return result, fmt.Errorf("package id mismatch: archive declares %q, expected %q", meta.id, expected.ID)
	}
	if expected.Version != "" && manifest.Version.String() != expected.Version {
		return result, fmt.Errorf("package version mismatch: archive declares %q, expected %q", manifest.Version, expected.Version)
	}
	platform := expected.Platform
	if meta.v2 {
		if platform != "" && meta.platform != platform {
			return result, fmt.Errorf("package platform mismatch: archive declares %q, expected %q", meta.platform, platform)
		}
		platform = meta.platform
	}
	if platform == "" {
		platform = PlatformTuple()
	}
	if err := validatePackageFileReferences(manifest, files, meta.v2); err != nil {
		return result, err
	}
	if _, exists := files[strings.ToLower(installReceiptName)]; exists {
		return result, fmt.Errorf("package archive uses reserved file name %q", installReceiptName)
	}

	manifest.DriverInfo.ID = runtimeID
	manifest.DriverInfo.Source = "dbc"
	installedHash := ""
	if manifest.Files.Driver != "" {
		manifest.DriverInfo.Driver.Shared.Set(platform, filepath.Join(finalDir, manifest.Files.Driver))
		installedHash, err = hashFile(filepath.Join(payloadDir, manifest.Files.Driver))
		if err != nil {
			return result, fmt.Errorf("could not hash installed driver file: %w", err)
		}
	} else {
		// Preserve explicit legacy runtime load paths. Older packages that omit
		// Driver.shared rely on the package directory as their fallback.
		if !hasRuntimeSharedPath(manifest.DriverInfo.Driver.Shared) {
			manifest.DriverInfo.Driver.Shared.Set(platform, finalDir)
		}
	}
	receipt := InstallReceipt{
		SourceType: expected.SourceType, SourceIdentity: expected.SourceIdentity,
		DriverID: runtimeID, DriverVersion: manifest.Version.String(), Platform: platform,
		ArchiveHash: archiveHash, ArchiveSize: archiveSize, InstalledLibraryHash: installedHash,
	}
	if err := writeInstallReceipt(payloadDir, receipt); err != nil {
		return result, fmt.Errorf("could not write installation receipt: %w", err)
	}
	if err := os.Chmod(payloadDir, 0o755); err != nil {
		return result, fmt.Errorf("could not prepare package directory for publication: %w", err)
	}
	if err := publishDirectory(payloadDir, finalDir); err != nil {
		return result, fmt.Errorf("could not publish package directory: %w", err)
	}
	return manifest, nil
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
	manifestCount := 0
	var parsed packageManifest
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
		if strings.EqualFold(header.Name, "MANIFEST") && header.Name != "MANIFEST" {
			return empty, packageManifest{}, nil, fmt.Errorf("package manifest must be named exactly MANIFEST")
		}
		if header.Name == "MANIFEST" {
			manifestCount++
			if header.Size > 1<<20 {
				return empty, packageManifest{}, nil, errors.New("package manifest exceeds 1 MiB")
			}
			data, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
			if err != nil {
				return empty, packageManifest{}, nil, fmt.Errorf("could not read package manifest: %w", err)
			}
			if int64(len(data)) != header.Size {
				return empty, packageManifest{}, nil, errors.New("package manifest size does not match its tar header")
			}
			parsed, err = decodePackageManifest(data)
			if err != nil {
				return empty, packageManifest{}, nil, err
			}
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
	if manifestCount != 1 {
		return empty, packageManifest{}, nil, fmt.Errorf("package archive must contain exactly one MANIFEST; found %d", manifestCount)
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
