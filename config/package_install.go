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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/Masterminds/semver/v3"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

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
		return PackageValidation{}, errors.New("package v2 requires archive hash metadata")
	}
	finalExpected := requested
	finalExpected.PackageVersion = manifest.PackageVersion
	finalExpected.ArchiveHash = receipt.ArchiveHash
	finalExpected.ArchiveSize = receipt.ArchiveSize
	finalExpected.ArchiveSizePresent = true
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
		expected.SourceType != "" && expected.SourceIdentity != "" && expected.ArchiveHash != "" &&
		receipt.DriverID == expected.ID && receipt.DriverVersion == expected.Version &&
		receipt.Platform == expected.Platform && receipt.SourceType == expected.SourceType &&
		receipt.SourceIdentity == expected.SourceIdentity && receipt.ArchiveHash == expected.ArchiveHash &&
		(!expected.hasExpectedArchiveSize() || receipt.ArchiveSize == expected.ArchiveSize) &&
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
	if expected.ArchiveHash != "" || expected.hasExpectedArchiveSize() {
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
