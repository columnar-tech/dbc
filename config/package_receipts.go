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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Masterminds/semver/v3"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

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
