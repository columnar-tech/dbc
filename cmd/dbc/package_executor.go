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

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/columnar-tech/dbc/internal/resolution"
	"github.com/columnar-tech/dbc/internal/sourceresolution"
)

// packageExecutor owns the package-level proof and installation lifecycle for
// one selected artifact. Sync continues to own planning, project locking, and
// candidate-lock persistence, while this executor handles the source-neutral
// transition from a resolved artifact to a verified runtime registration.
type packageExecutor struct {
	cfg              config.Config
	baseDir          string
	noVerify         bool
	downloadArtifact func(context.Context, dbc.PkgInfo) (io.ReadCloser, error)
	downloadPkg      func(dbc.PkgInfo) (*os.File, error)
	fetchPackslip    func(context.Context, *url.URL) (io.ReadCloser, error)
	ensurePackage    func(context.Context, config.Config, string, config.ExpectedPackageMetadata, config.EnsurePackageCallbacks) (config.EnsurePackageResult, error)
}

func newPackageExecutor(cfg config.Config, baseDir string, noVerify bool,
	downloadArtifact func(context.Context, dbc.PkgInfo) (io.ReadCloser, error),
	downloadPkg func(dbc.PkgInfo) (*os.File, error),
	fetchPackslip func(context.Context, *url.URL) (io.ReadCloser, error),
	ensurePackage func(context.Context, config.Config, string, config.ExpectedPackageMetadata, config.EnsurePackageCallbacks) (config.EnsurePackageResult, error),
) *packageExecutor {
	if ensurePackage == nil {
		ensurePackage = config.EnsurePackage
	}
	if fetchPackslip == nil {
		fetchPackslip = fetchPackslipArtifact
	}
	return &packageExecutor{
		cfg: cfg, baseDir: baseDir, noVerify: noVerify,
		downloadArtifact: downloadArtifact, downloadPkg: downloadPkg,
		fetchPackslip: fetchPackslip,
		ensurePackage: ensurePackage,
	}
}

func (s syncModel) newPackageExecutor() (*packageExecutor, error) {
	baseDir := ""
	if s.Path != "" || s.LockFilePath != "" {
		var err error
		baseDir, err = s.projectBaseDir()
		if err != nil {
			return nil, err
		}
	}
	var ensurePackage func(context.Context, config.Config, string, config.ExpectedPackageMetadata, config.EnsurePackageCallbacks) (config.EnsurePackageResult, error)
	if s.worker != nil {
		ensurePackage = s.worker.hooks.ensurePackage
	}
	return newPackageExecutor(s.cfg, baseDir, s.NoVerify, s.downloadArtifact, s.downloadPkg, s.fetchPackslipArtifact, ensurePackage), nil
}

func validateInstallableArtifactFormat(format string) error {
	switch format {
	case "", "tar.gz", "tgz":
		return nil
	default:
		return fmt.Errorf("unsupported package format %q (supported formats: tar.gz, tgz)", format)
	}
}

func validateHostRequirements(driverID string, requirements resolution.HostRequirements) error {
	if requirements.OSMin == "" && requirements.GLibCMin == "" && len(requirements.Libs) == 0 && len(requirements.Bins) == 0 {
		return nil
	}
	var declared []string
	if requirements.OSMin != "" {
		declared = append(declared, fmt.Sprintf("os_min=%q", requirements.OSMin))
	}
	if requirements.GLibCMin != "" {
		declared = append(declared, fmt.Sprintf("glibc_min=%q", requirements.GLibCMin))
	}
	if len(requirements.Libs) != 0 {
		libs := append([]string(nil), requirements.Libs...)
		sort.Strings(libs)
		quoted := make([]string, len(libs))
		for i, lib := range libs {
			quoted[i] = fmt.Sprintf("%q", lib)
		}
		declared = append(declared, "libs=["+strings.Join(quoted, ", ")+"]")
	}
	if len(requirements.Bins) != 0 {
		bins := append([]resolution.NamedRequirement(nil), requirements.Bins...)
		sort.Slice(bins, func(i, j int) bool {
			if bins[i].Name != bins[j].Name {
				return bins[i].Name < bins[j].Name
			}
			return bins[i].Min < bins[j].Min
		})
		formatted := make([]string, len(bins))
		for i, bin := range bins {
			formatted[i] = fmt.Sprintf("%q", bin.Name)
			if bin.Min != "" {
				formatted[i] += fmt.Sprintf(" (min %q)", bin.Min)
			}
		}
		declared = append(declared, "bins=["+strings.Join(formatted, ", ")+"]")
	}
	return fmt.Errorf("unsupported host requirements for %s: %s", driverID, strings.Join(declared, "; "))
}

func expectedSyncPackageMetadata(item installItem) (config.ExpectedPackageMetadata, bool, error) {
	selected, err := item.selectedArtifact()
	if err != nil {
		return config.ExpectedPackageMetadata{}, false, err
	}
	if item.Release.DriverID == "" || item.Release.Version == "" || item.Platform == "" ||
		item.Release.Source.Type == "" || item.Release.Source.Reference == "" {
		return config.ExpectedPackageMetadata{}, false, errors.New("resolved package metadata is incomplete")
	}
	hasHash := selected.Hash != ""
	hasSize := selected.Size != nil
	if hasHash != hasSize {
		return config.ExpectedPackageMetadata{}, false, errors.New("package metadata must include both archive hash and size")
	}
	if selected.PackageVersion == 2 && !hasHash {
		return config.ExpectedPackageMetadata{}, false, errors.New("package v2 requires archive hash and size metadata")
	}
	expected := config.ExpectedPackageMetadata{
		ID: item.Release.DriverID, Version: item.Release.Version, Platform: item.Platform,
		SourceType: item.Release.Source.Type, SourceIdentity: item.Release.Source.Reference,
		PackageVersion: selected.PackageVersion,
	}
	if hasHash {
		expected.ArchiveHash = selected.Hash
		expected.ArchiveSize = *selected.Size
	}
	return expected, hasHash, nil
}

func (e *packageExecutor) openResolvedArtifact(ctx context.Context, item installItem) (*sourceresolution.OpenedArtifact, error) {
	selected, err := item.selectedArtifact()
	if err != nil {
		return nil, err
	}
	if selected.Location.Kind == resolution.ArtifactLocationPath {
		if item.Release.Source.Type != "path" {
			return nil, fmt.Errorf("source %q cannot open a path artifact", item.Release.Source.Type)
		}
		return sourceresolution.OpenArtifact(ctx, nil, selected.Location, e.baseDir)
	}
	if selected.Location.Kind != resolution.ArtifactLocationURL {
		return nil, fmt.Errorf("unsupported artifact location kind %q", selected.Location.Kind)
	}
	switch item.Release.Source.Type {
	case "packslip":
		if e.fetchPackslip == nil {
			return nil, errors.New("no credential-free Packslip artifact fetcher is configured")
		}
		return sourceresolution.OpenArtifact(ctx, e.fetchPackslip, selected.Location, e.baseDir)
	case "registry":
		fetch := func(ctx context.Context, artifactURL *url.URL) (io.ReadCloser, error) {
			return e.openRegistryArtifact(ctx, item, selected, artifactURL)
		}
		return sourceresolution.OpenArtifact(ctx, fetch, selected.Location, e.baseDir)
	default:
		return nil, fmt.Errorf("source %q cannot open a URL artifact", item.Release.Source.Type)
	}
}

func (e *packageExecutor) openRegistryArtifact(ctx context.Context, item installItem, selected *resolution.Artifact, artifactURL *url.URL) (io.ReadCloser, error) {
	version, err := semver.NewVersion(item.Release.Version)
	if err != nil {
		return nil, fmt.Errorf("invalid resolved package version %q: %w", item.Release.Version, err)
	}
	registryURL, err := url.Parse(item.Release.Source.Reference)
	if err != nil || registryURL.Host == "" {
		return nil, fmt.Errorf("invalid resolved registry identity %q", item.Release.Source.Reference)
	}
	pkg := dbc.PkgInfo{
		Driver:        dbc.Driver{Path: item.Release.DriverID, Title: item.Release.DriverID, Registry: &dbc.Registry{BaseURL: registryURL}},
		Version:       version,
		PlatformTuple: item.Platform,
		Path:          artifactURL,
		ArtifactHash:  selected.Hash,
		ArtifactSize:  cloneInt64(selected.Size),
	}
	if e.downloadArtifact != nil {
		return e.downloadArtifact(ctx, pkg)
	}
	if e.downloadPkg == nil {
		return nil, errors.New("no artifact downloader is configured")
	}
	// Existing test adapters and custom models expose the legacy file-based
	// hook. Production sync uses downloadArtifact, which calls Client.Download.
	return e.downloadPkg(pkg)
}

func (e *packageExecutor) prepareItem(ctx context.Context, item *installItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	selected, err := item.selectedArtifact()
	if err != nil {
		return err
	}
	if err := validateInstallableArtifactFormat(selected.Format); err != nil {
		return fmt.Errorf("driver %s: %w", item.Release.DriverID, err)
	}
	if err := validateHostRequirements(item.Release.DriverID, selected.HostRequirements); err != nil {
		return err
	}
	var sameVersionInstalled *config.DriverInfo
	if e.cfg.Exists {
		if installed, ok := e.cfg.Drivers[item.Release.DriverID]; ok {
			if installed.Version != nil && item.Release.Version == installed.Version.String() {
				installedCopy := installed
				sameVersionInstalled = &installedCopy
				expected, _, err := expectedSyncPackageMetadata(*item)
				if err != nil {
					return err
				}
				item.Expected = expected
				matches, err := e.itemCurrentMatches(item, &installedCopy)
				if err != nil {
					return err
				}
				if matches {
					libraryHash, hashErr := checksum(installed.Driver.Shared.Get(item.Platform))
					if hashErr != nil {
						return fmt.Errorf("failed to checksum installed driver: %w", hashErr)
					}
					markAlreadyInstalled(item, installed, libraryHash)
				}
			}
		}
	}

	needsArchive := item.AlreadyInstalled == nil || !canReuseLockedEntry(*item)
	if needsArchive {
		if err := e.downloadAndValidateItem(ctx, item); err != nil {
			return err
		}
		if sameVersionInstalled != nil {
			matches, err := e.itemCurrentMatches(item, sameVersionInstalled)
			if err != nil {
				return err
			}
			if matches {
				libraryHash, hashErr := checksum(sameVersionInstalled.Driver.Shared.Get(item.Platform))
				if hashErr != nil {
					return fmt.Errorf("failed to checksum installed driver: %w", hashErr)
				}
				markAlreadyInstalled(item, *sameVersionInstalled, libraryHash)
			}
		}
		if item.AlreadyInstalled == nil {
			item.InstalledLibraryHash = item.ValidatedLibraryHash
			if item.Validation != nil && item.Validation.VerifiedLibraryHash == "" &&
				item.LockEntry != nil && item.LockEntry.Legacy != nil && samePlatformTarget(item.LockEntry.Legacy.Platform, item.Platform) {
				item.InstalledLibraryHash = item.LockEntry.Legacy.LibraryHash
			}
		}
	} else {
		// The exact locked artifact is already proven by its managed receipt.
		expected, _, err := expectedSyncPackageMetadata(*item)
		if err != nil {
			return err
		}
		item.Expected = expected
	}
	return nil
}

func (e *packageExecutor) downloadAndValidateItem(ctx context.Context, item *installItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := item.selectedArtifact(); err != nil {
		return err
	}
	expected, _, err := expectedSyncPackageMetadata(*item)
	if err != nil {
		return err
	}
	if item.Archive == nil {
		archive, err := e.openResolvedArtifact(ctx, *item)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("failed to open driver artifact: %w", err)
		}
		item.Archive = archive
	}
	archive := item.Archive.File
	var verify func(string, config.Manifest) error
	if !e.noVerify {
		verify = func(stagingDir string, manifest config.Manifest) error {
			return dbc.VerifyPackageSignature(stagingDir, manifest)
		}
	}
	validation, err := config.PreparePackage(e.cfg, item.Release.DriverID, archive, expected, config.InstallOptions{Verify: verify})
	if err != nil {
		if isPackageVerificationFailure(err) {
			return fmt.Errorf("failed to verify signature: %w", packageVerificationError(err))
		}
		return fmt.Errorf("failed to prepare driver package: %w", err)
	}
	prepared := validation.Prepared
	preparedAccepted := false
	defer func() {
		if !preparedAccepted && prepared != nil {
			_ = prepared.Close()
		}
	}()
	selected, err := item.selectedArtifact()
	if err != nil {
		return err
	}
	if selected.Hash == "" {
		selected.Hash = validation.ArchiveHash
		selected.Size = cloneInt64(&validation.ArchiveSize)
	}
	selected.PackageVersion = validation.PackageVersion
	expected, _, err = expectedSyncPackageMetadata(*item)
	if err != nil {
		return err
	}
	item.Expected = expected
	item.ValidatedLibraryHash = strings.TrimPrefix(validation.VerifiedLibraryHash, "sha256:")
	if item.ValidatedLibraryHash == "" {
		candidateLibrary := validation.Registration.Driver.Shared.Get(item.Platform)
		if item.LockEntry != nil && item.LockEntry.Legacy != nil && samePlatformTarget(item.LockEntry.Legacy.Platform, item.Platform) {
			if err := verifyLegacyExternalLibrary(candidateLibrary, item.LockEntry.Legacy.LibraryHash); err != nil {
				return fmt.Errorf("candidate package external library does not match the legacy lock proof: %w", err)
			}
		}
	}
	if item.Archive != nil {
		if err := item.Archive.Close(); err != nil {
			return fmt.Errorf("could not close downloaded package archive: %w", err)
		}
		item.Archive = nil
	}
	item.Validation = &validation
	preparedAccepted = true
	return nil
}

func (e *packageExecutor) itemCurrentMatches(item *installItem, current *config.DriverInfo) (bool, error) {
	if current == nil || current.Version == nil || item.Expected.ID == "" || item.Expected.Version == "" ||
		current.ID != item.Expected.ID || current.Version.String() != item.Expected.Version {
		return false, nil
	}
	receipt, managed, present, valid, err := config.InspectDriverInstallReceipt(e.cfg, *current)
	if err != nil {
		return false, fmt.Errorf("failed to resolve installed driver receipt location: %w", err)
	}
	libraryPath := current.Driver.Shared.Get(item.Platform)
	if managed && present {
		if !valid || !config.InstallReceiptMatchesExpectedPackage(receipt, item.Expected) ||
			!config.VerifyInstallReceiptLibraryIntegrity(libraryPath, receipt) ||
			!config.InstallReceiptMatchesRuntimeRegistration(receipt, *current, item.Platform) {
			return false, nil
		}
		if item.Validation != nil {
			if item.Validation.VerifiedLibraryHash == "" || item.Validation.VerifiedLibraryHash != receipt.InstalledLibraryHash ||
				!config.PackageValidationMatchesRuntimeRegistration(*current, *item.Validation, item.Platform) {
				return false, nil
			}
		}
		return true, nil
	}
	if present || item.Validation == nil || item.LockEntry == nil || item.LockEntry.Legacy == nil ||
		!samePlatformTarget(item.LockEntry.Legacy.Platform, item.Platform) ||
		!config.PackageValidationMatchesRuntimeRegistration(*current, *item.Validation, item.Platform) {
		return false, nil
	}
	proofHash := item.LockEntry.Legacy.LibraryHash
	if err := validateLegacyLibraryHash(proofHash); err != nil {
		return false, nil
	}
	currentHash, err := checksum(libraryPath)
	if err != nil || currentHash != proofHash {
		return false, nil
	}
	if item.Validation.VerifiedLibraryHash != "" {
		return item.ValidatedLibraryHash == proofHash, nil
	}
	if err := verifyLegacyExternalLibrary(item.Validation.Registration.Driver.Shared.Get(item.Platform), proofHash); err != nil {
		return false, nil
	}
	return true, nil
}

func (e *packageExecutor) ensurePreparedPackage(ctx context.Context, item *installItem) (config.EnsurePackageResult, error) {
	if item != nil && item.Validation != nil && item.Validation.Prepared != nil {
		defer func() { _ = item.Validation.Prepared.Close() }()
	}
	if err := ctx.Err(); err != nil {
		return config.EnsurePackageResult{}, err
	}
	if _, err := item.selectedArtifact(); err != nil {
		return config.EnsurePackageResult{}, err
	}
	callbacks := config.EnsurePackageCallbacks{
		CurrentMatches: func(current *config.DriverInfo) (bool, error) {
			return e.itemCurrentMatches(item, current)
		},
		Prepare: func(ctx context.Context) (*config.PreparedPackage, error) {
			if item.Validation == nil || item.Validation.Prepared == nil {
				if err := e.downloadAndValidateItem(ctx, item); err != nil {
					return nil, err
				}
			}
			if item.Validation == nil || item.Validation.Prepared == nil {
				return nil, errors.New("package preparation completed without a prepared payload")
			}
			return item.Validation.Prepared, nil
		},
		ValidateResult: func(result config.EnsurePackageResult) error {
			return e.validateEnsureResult(item, result)
		},
	}
	result, err := e.ensurePackage(ctx, e.cfg, item.Release.DriverID, item.Expected, callbacks)
	if err != nil && isPackageVerificationFailure(err) {
		return result, fmt.Errorf("failed to verify signature: %w", packageVerificationError(err))
	}
	return result, err
}

func (e *packageExecutor) validateEnsureResult(item *installItem, result config.EnsurePackageResult) error {
	if result.Installed == nil {
		return errors.New("package ensure returned no installed driver registration")
	}
	if result.Skipped {
		matches, err := e.itemCurrentMatches(item, result.Installed)
		if err != nil {
			return err
		}
		if !matches {
			return errors.New("installed driver no longer matches the selected package proof")
		}
		return nil
	}
	if item.Validation == nil || result.Manifest == nil {
		return errors.New("installed package is missing validated candidate evidence")
	}
	if !config.PackageValidationMatchesRuntimeRegistration(*result.Installed, *item.Validation, item.Platform) ||
		!config.PackageValidationMatchesRuntimeRegistration(result.Manifest.DriverInfo, *item.Validation, item.Platform) {
		return errors.New("installed registration does not match the validated package")
	}
	if result.Installed.Version == nil || result.Installed.Version.String() != item.Expected.Version {
		return errors.New("installed driver version does not match the selected package")
	}
	if item.Validation.VerifiedLibraryHash == "" {
		path := result.Installed.Driver.Shared.Get(item.Platform)
		if item.LockEntry != nil && item.LockEntry.Legacy != nil && samePlatformTarget(item.LockEntry.Legacy.Platform, item.Platform) {
			if err := verifyLegacyExternalLibrary(path, item.LockEntry.Legacy.LibraryHash); err != nil {
				return fmt.Errorf("installed package external library does not match the legacy lock proof: %w", err)
			}
		}
		return nil
	}
	receipt, managed, present, valid, err := config.InspectDriverInstallReceipt(e.cfg, *result.Installed)
	if err != nil {
		return fmt.Errorf("failed to resolve installed driver receipt location: %w", err)
	}
	libraryPath := result.Installed.Driver.Shared.Get(item.Platform)
	if !managed || !present || !valid || !config.InstallReceiptMatchesExpectedPackage(receipt, item.Expected) ||
		receipt.InstalledLibraryHash != item.Validation.VerifiedLibraryHash ||
		!config.InstallReceiptMatchesRuntimeRegistration(receipt, *result.Installed, item.Platform) ||
		!config.VerifyInstallReceiptLibraryIntegrity(libraryPath, receipt) {
		return errors.New("installed package receipt does not match the validated package")
	}
	return nil
}

func markAlreadyInstalled(item *installItem, installed config.DriverInfo, libraryHash string) {
	item.InstalledLibraryHash = strings.TrimPrefix(libraryHash, "sha256:")
	installedCopy := installed
	item.AlreadyInstalled = &installedCopy
}

func verifyLegacyExternalLibrary(path, expectedHash string) error {
	if err := validateLegacyLibraryHash(expectedHash); err != nil {
		return fmt.Errorf("invalid legacy library checksum: %w", err)
	}
	if path == "" {
		return errors.New("candidate runtime registration has no shared library path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("could not inspect candidate external library %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("candidate external library %s is not a regular file", path)
	}
	actualHash, err := checksum(path)
	if err != nil {
		return err
	}
	if actualHash != expectedHash {
		return fmt.Errorf("candidate external library checksum mismatch: got %s, expected %s", actualHash, expectedHash)
	}
	return nil
}
