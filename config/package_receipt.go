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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/columnar-tech/dbc/internal/hostpath"

	"github.com/Masterminds/semver/v3"
)

const (
	packageInstallReceiptFilename = "dbc-install-receipt.json"
	packageInstallReceiptVersion  = 1
	registrationFingerprintName   = "sha256"
	registrationFingerprintVer    = 1
	packageInstallReceiptMaxSize  = 16 * 1024
)

type packageLibraryKind string

type packageRegistrationScope string

const (
	packageLibraryFile     packageLibraryKind = "package_file"
	packageLibraryExternal packageLibraryKind = "external"

	packageRegistrationFile           packageRegistrationScope = "file"
	packageRegistrationRegistryUser   packageRegistrationScope = "registry-user"
	packageRegistrationRegistrySystem packageRegistrationScope = "registry-system"
)

// packageInstallReceipt records local ownership and integrity evidence. It
// deliberately contains no source identity or archive provenance.
// TODO: Define and record source/archive evidence before strict locked-artifact
// reuse; OwnedLibrarySHA256 verifies local installed bytes only and does not
// establish upstream artifact identity.
type packageInstallReceipt struct {
	SchemaVersion                int                      `json:"schema_version"`
	RegistrationScope            packageRegistrationScope `json:"registration_scope"`
	RuntimeID                    string                   `json:"runtime_id"`
	DriverVersion                string                   `json:"driver_version"`
	Platform                     string                   `json:"platform"`
	Generation                   string                   `json:"generation"`
	LibraryKind                  packageLibraryKind       `json:"library_kind"`
	OwnedLibraryFilename         string                   `json:"owned_library_filename,omitempty"`
	OwnedLibrarySHA256           string                   `json:"owned_library_sha256,omitempty"`
	RegistrationFingerprintAlgo  string                   `json:"registration_fingerprint_algorithm"`
	RegistrationFingerprintVer   int                      `json:"registration_fingerprint_version"`
	RegistrationFingerprintValue string                   `json:"registration_fingerprint"`
}

type registrationFingerprintDTO struct {
	Version    int                         `json:"version"`
	RuntimeID  string                      `json:"runtime_id"`
	Platform   string                      `json:"platform"`
	Source     string                      `json:"source"`
	Name       string                      `json:"name"`
	Publisher  string                      `json:"publisher"`
	License    string                      `json:"license"`
	Entrypoint string                      `json:"entrypoint"`
	DriverVer  string                      `json:"driver_version"`
	ADBCVer    *string                     `json:"adbc_version,omitempty"`
	Features   *registrationFeatureSetsDTO `json:"features,omitempty"`
	Shared     registrationSharedIdentity  `json:"shared"`
}

type registrationFeatureSetsDTO struct {
	Supported   []string `json:"supported"`
	Unsupported []string `json:"unsupported"`
}

type registrationSharedIdentity struct {
	Kind  packageLibraryKind `json:"kind"`
	Value string             `json:"value"`
}

var packagePlatformPattern = regexp.MustCompile(`^[a-z0-9]+_[a-z0-9]+$`)

const packageGenerationNamePrefix = ".dbc-package-g-"

func packageGenerationPrefix(runtimeID string) (string, error) {
	if err := validatePackageFilename(runtimeID); err != nil {
		return "", err
	}
	return packageGenerationNamePrefix + strconv.Itoa(len([]byte(runtimeID))) + "-" + runtimeID + "-", nil
}

func parsePackageGenerationName(name string) (string, bool) {
	if !strings.HasPrefix(name, packageGenerationNamePrefix) {
		return "", false
	}
	rest := strings.TrimPrefix(name, packageGenerationNamePrefix)
	separator := strings.IndexByte(rest, '-')
	if separator <= 0 {
		return "", false
	}
	lengthText := rest[:separator]
	length, err := strconv.Atoi(lengthText)
	if err != nil || length <= 0 || strconv.Itoa(length) != lengthText {
		return "", false
	}
	identityAndSuffix := rest[separator+1:]
	if len(identityAndSuffix) <= length || identityAndSuffix[length] != '-' {
		return "", false
	}
	runtimeID := identityAndSuffix[:length]
	if !utf8.ValidString(runtimeID) || validatePackageFilename(runtimeID) != nil || identityAndSuffix[length+1:] == "" {
		return "", false
	}
	return runtimeID, true
}

func validPackageRegistrationScope(scope packageRegistrationScope) bool {
	switch scope {
	case packageRegistrationFile, packageRegistrationRegistryUser, packageRegistrationRegistrySystem:
		return true
	default:
		return false
	}
}

func makePackageInstallReceipt(cfg Config, stagingDir, generation, runtimeID, platform string, manifest Manifest) (packageInstallReceipt, error) {
	scope, err := packageRegistrationScopeForConfig(cfg)
	if err != nil {
		return packageInstallReceipt{}, fmt.Errorf("resolve package registration scope: %w", err)
	}
	if err := validatePackageFilename(runtimeID); err != nil {
		return packageInstallReceipt{}, fmt.Errorf("invalid runtime ID: %w", err)
	}
	if !packagePlatformPattern.MatchString(platform) {
		return packageInstallReceipt{}, fmt.Errorf("invalid package platform %q", platform)
	}
	if manifest.Version == nil {
		return packageInstallReceipt{}, errors.New("package manifest has no driver version")
	}
	if manifest.DriverInfo.ID != runtimeID {
		return packageInstallReceipt{}, errors.New("package manifest runtime ID does not match receipt ID")
	}
	version := manifest.Version.String()
	kind := packageLibraryExternal
	filename, libraryHash := "", ""
	sharedIdentity := manifest.Driver.Shared.Get(platform)
	if manifest.Files.Driver != "" {
		kind = packageLibraryFile
		filename = manifest.Files.Driver
		if err := validateOwnedPackageFilename(filename); err != nil {
			return packageInstallReceipt{}, fmt.Errorf("invalid owned library filename: %w", err)
		}
		sharedIdentity = filename
		file, err := os.Open(hostpath.Join(stagingDir, filename))
		if err != nil {
			return packageInstallReceipt{}, fmt.Errorf("open package library for receipt: %w", err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return packageInstallReceipt{}, fmt.Errorf("hash package library for receipt: %w", copyErr)
		}
		if closeErr != nil {
			return packageInstallReceipt{}, fmt.Errorf("close package library after hashing: %w", closeErr)
		}
		libraryHash = hex.EncodeToString(hash.Sum(nil))
	} else if sharedIdentity == "" {
		return packageInstallReceipt{}, fmt.Errorf("manifest-only package has no shared library for platform %s", platform)
	}
	fingerprint, err := runtimeRegistrationFingerprint(cfg, runtimeID, platform, manifest.DriverInfo, kind, sharedIdentity)
	if err != nil {
		return packageInstallReceipt{}, err
	}
	return packageInstallReceipt{
		SchemaVersion:                packageInstallReceiptVersion,
		RegistrationScope:            scope,
		RuntimeID:                    runtimeID,
		DriverVersion:                version,
		Platform:                     platform,
		Generation:                   generation,
		LibraryKind:                  kind,
		OwnedLibraryFilename:         filename,
		OwnedLibrarySHA256:           libraryHash,
		RegistrationFingerprintAlgo:  registrationFingerprintName,
		RegistrationFingerprintVer:   registrationFingerprintVer,
		RegistrationFingerprintValue: fingerprint,
	}, nil
}

func runtimeRegistrationFingerprint(cfg Config, runtimeID, platform string, info DriverInfo, kind packageLibraryKind, sharedValue string) (string, error) {
	if info.Version == nil {
		return "", errors.New("runtime registration has no driver version")
	}
	sharedKind := kind
	if sharedKind != packageLibraryFile && sharedKind != packageLibraryExternal {
		return "", fmt.Errorf("invalid runtime library kind %q", sharedKind)
	}
	driverVersion := info.Version.String()
	if sharedKind == packageLibraryFile {
		if err := validateOwnedPackageFilename(sharedValue); err != nil {
			return "", fmt.Errorf("invalid package library identity: %w", err)
		}
	}
	dto := registrationFingerprintDTO{
		Version:    registrationFingerprintVer,
		RuntimeID:  runtimeID,
		Platform:   platform,
		Source:     info.Source,
		Name:       info.Name,
		Publisher:  info.Publisher,
		License:    info.License,
		Entrypoint: info.Driver.Entrypoint,
		DriverVer:  driverVersion,
		Shared:     registrationSharedIdentity{Kind: sharedKind, Value: sharedValue},
	}
	if registrationFingerprintIncludesADBC(cfg) {
		features := registrationFeatureSetsDTO{
			Supported:   normalizeRegistrationFeatures(info.AdbcInfo.Features.Supported),
			Unsupported: normalizeRegistrationFeatures(info.AdbcInfo.Features.Unsupported),
		}
		dto.Features = &features
		if info.AdbcInfo.Version != nil {
			version := info.AdbcInfo.Version.String()
			dto.ADBCVer = &version
		}
	}
	data, err := json.Marshal(dto)
	if err != nil {
		return "", fmt.Errorf("encode runtime registration fingerprint: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeRegistrationFeatures(features []string) []string {
	if len(features) == 0 {
		return []string{}
	}
	unique := make(map[string]struct{}, len(features))
	for _, feature := range features {
		unique[feature] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for feature := range unique {
		result = append(result, feature)
	}
	sort.Strings(result)
	return result
}

func writePackageInstallReceipt(stagingDir string, receipt packageInstallReceipt) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode package install receipt: %w", err)
	}
	data = append(data, '\n')
	if len(data) > packageInstallReceiptMaxSize {
		return fmt.Errorf("package install receipt exceeds maximum size of %d bytes: got %d", packageInstallReceiptMaxSize, len(data))
	}
	path := hostpath.Join(stagingDir, packageInstallReceiptFilename)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create package install receipt: %w", err)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write package install receipt: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close package install receipt: %w", closeErr)
	}
	return nil
}

// readPackageInstallReceipt accepts evidence only when its identity is
// structurally consistent with a direct generation under the absolute primary
// root. Invalid or absent evidence is treated as no ownership proof.
func readPackageInstallReceipt(primaryRoot, generationDir string) (packageInstallReceipt, bool) {
	var empty packageInstallReceipt
	root, err := hostpath.Abs(primaryRoot)
	if err != nil {
		return empty, false
	}
	generation, err := hostpath.Abs(generationDir)
	if err != nil {
		return empty, false
	}
	root = hostpath.Clean(root)
	generation = hostpath.Clean(generation)
	if hostpath.Dir(generation) != root {
		return empty, false
	}
	parent, err := os.OpenRoot(root)
	if err != nil {
		return empty, false
	}
	defer parent.Close()
	generationName := hostpath.Base(generation)
	generationInfo, err := parent.Lstat(generationName)
	if err != nil || !generationInfo.IsDir() || generationInfo.Mode()&os.ModeSymlink != 0 {
		return empty, false
	}
	generationRoot, err := parent.OpenRoot(generationName)
	if err != nil {
		return empty, false
	}
	defer generationRoot.Close()
	openedGenerationInfo, err := generationRoot.Stat(".")
	if err != nil || !os.SameFile(generationInfo, openedGenerationInfo) {
		return empty, false
	}
	return readPackageInstallReceiptAtRoot(generationRoot, generationName)
}

// readPackageInstallReceiptAtRoot reads ownership evidence relative to an
// already verified generation handle. Cleanup keeps this handle open through
// the subsequent payload deletion so it never reopens a checked pathname.
func readPackageInstallReceiptAtRoot(generationRoot *os.Root, generationName string) (packageInstallReceipt, bool) {
	var empty packageInstallReceipt
	info, err := generationRoot.Lstat(packageInstallReceiptFilename)
	if err != nil || !info.Mode().IsRegular() {
		return empty, false
	}
	file, err := generationRoot.Open(packageInstallReceiptFilename)
	if err != nil {
		return empty, false
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return empty, false
	}
	data, err := io.ReadAll(io.LimitReader(file, packageInstallReceiptMaxSize+1))
	if err != nil || len(data) > packageInstallReceiptMaxSize {
		return empty, false
	}
	var receipt packageInstallReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&receipt) != nil {
		return empty, false
	}
	if decoder.Decode(new(any)) != io.EOF {
		return empty, false
	}
	if !validPackageInstallReceiptForGeneration(receipt, generationName) {
		return empty, false
	}
	return receipt, true
}

func validPackageInstallReceipt(receipt packageInstallReceipt, primaryRoot, generationDir string) bool {
	if receipt.SchemaVersion != packageInstallReceiptVersion || receipt.RegistrationFingerprintAlgo != registrationFingerprintName || receipt.RegistrationFingerprintVer != registrationFingerprintVer {
		return false
	}
	if validatePackageFilename(receipt.RuntimeID) != nil || !packagePlatformPattern.MatchString(receipt.Platform) {
		return false
	}
	if !validPackageRegistrationScope(receipt.RegistrationScope) {
		return false
	}
	version, err := semver.NewVersion(receipt.DriverVersion)
	if err != nil || version.String() != receipt.DriverVersion {
		return false
	}
	if hostpath.Dir(generationDir) != primaryRoot {
		return false
	}
	return validPackageInstallReceiptForGeneration(receipt, hostpath.Base(generationDir))
}

func validPackageInstallReceiptForGeneration(receipt packageInstallReceipt, generationName string) bool {
	if receipt.SchemaVersion != packageInstallReceiptVersion || receipt.RegistrationFingerprintAlgo != registrationFingerprintName || receipt.RegistrationFingerprintVer != registrationFingerprintVer {
		return false
	}
	if validatePackageFilename(receipt.RuntimeID) != nil || !packagePlatformPattern.MatchString(receipt.Platform) || !validPackageRegistrationScope(receipt.RegistrationScope) {
		return false
	}
	version, err := semver.NewVersion(receipt.DriverVersion)
	if err != nil || version.String() != receipt.DriverVersion {
		return false
	}
	generationRuntimeID, validGeneration := parsePackageGenerationName(generationName)
	if receipt.Generation != generationName || !validGeneration || !sameRuntimeID(generationRuntimeID, receipt.RuntimeID) {
		return false
	}
	if len(receipt.RegistrationFingerprintValue) != sha256.Size*2 {
		return false
	}
	if _, err := hex.DecodeString(receipt.RegistrationFingerprintValue); err != nil || strings.ToLower(receipt.RegistrationFingerprintValue) != receipt.RegistrationFingerprintValue {
		return false
	}
	switch receipt.LibraryKind {
	case packageLibraryFile:
		if validateOwnedPackageFilename(receipt.OwnedLibraryFilename) != nil || len(receipt.OwnedLibrarySHA256) != sha256.Size*2 {
			return false
		}
		if _, err := hex.DecodeString(receipt.OwnedLibrarySHA256); err != nil || strings.ToLower(receipt.OwnedLibrarySHA256) != receipt.OwnedLibrarySHA256 {
			return false
		}
	case packageLibraryExternal:
		if receipt.OwnedLibraryFilename != "" || receipt.OwnedLibrarySHA256 != "" {
			return false
		}
	default:
		return false
	}
	return true
}

func validateOwnedPackageFilename(name string) error {
	if err := validatePackageFilename(name); err != nil {
		return err
	}
	if strings.EqualFold(name, packageInstallReceiptFilename) {
		return fmt.Errorf("package library filename %q is reserved", name)
	}
	return nil
}
