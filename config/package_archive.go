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
)

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
	if expected.hasExpectedArchiveSize() && expected.ArchiveSize != archiveSize {
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
