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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/internal/fslock"
	"github.com/columnar-tech/dbc/internal/hostpath"
)

// InstalledPackageExpectation describes the package a caller wants to reuse.
type InstalledPackageExpectation struct {
	// Version is the driver version the registration must have. Required.
	Version *semver.Version
	// ArchiveSHA256 is the lowercase hex SHA-256 (64 characters) of the archive
	// the package must have been installed from. Required.
	ArchiveSHA256 string
	// SignatureVerifier, when set, re-verifies the installed library and
	// signature before reuse using private copies from the verified generation.
	// Normally the driver lock is held; a read-only fallback instead rechecks
	// the observed installation after verification. It must not mutate drivers.
	SignatureVerifier PackageVerifier
	// LibrarySHA256, when set, is the lowercase hex SHA-256 the installed
	// driver library must have (for example a v1 lockfile checksum).
	LibrarySHA256 string
}

// CheckInstalledPackage reports whether runtimeID's current registration at
// cfg's level is a dbc-owned package generation that matches expect: the
// registration, its install receipt, the installed library bytes and the
// recorded archive digest must all agree. Any mismatch, missing, corrupt or
// legacy state yields (DriverInfo{}, false, nil) so the caller can reinstall.
// Errors are returned only for invalid input, context cancellation and lock
// failures. The check never modifies installation state and looks only at
// cfg's primary location. A signature verifier may cause a private temporary
// directory to be created for its path-based callback. On platforms without
// pinned directory handles it always reports false.
// If a lock file cannot be opened for writing due to permissions or a read-only
// filesystem, it checks an optimistic read-only snapshot instead. This is not
// exclusion against a concurrent writer; observed changes prevent reuse.
func CheckInstalledPackage(ctx context.Context, cfg Config, runtimeID string, expect InstalledPackageExpectation) (info DriverInfo, ok bool, err error) {
	if ctx == nil {
		return DriverInfo{}, false, errors.New("check context is nil")
	}
	if err := validatePackageFilename(runtimeID); err != nil {
		return DriverInfo{}, false, fmt.Errorf("invalid runtime ID: %w", err)
	}
	if expect.Version == nil {
		return DriverInfo{}, false, errors.New("expected package version is nil")
	}
	if !validArchiveSHA256(expect.ArchiveSHA256) {
		return DriverInfo{}, false, fmt.Errorf("invalid expected archive SHA-256 %q: want 64 lowercase hex characters", expect.ArchiveSHA256)
	}
	if expect.LibrarySHA256 != "" && !validArchiveSHA256(expect.LibrarySHA256) {
		return DriverInfo{}, false, fmt.Errorf("invalid expected library SHA-256 %q: want 64 lowercase hex characters", expect.LibrarySHA256)
	}
	if err := ctx.Err(); err != nil {
		return DriverInfo{}, false, err
	}
	if !supportsPinnedCleanup() {
		return DriverInfo{}, false, nil
	}

	location := cfg.PrimaryLocation()
	if location == "" {
		return DriverInfo{}, false, nil
	}
	location, err = hostpath.Abs(location)
	if err != nil {
		return DriverInfo{}, false, fmt.Errorf("resolve config location: %w", err)
	}
	location = hostpath.Clean(location)
	lockLocation, err := packageInstallLockLocation(cfg, location)
	if err != nil {
		return DriverInfo{}, false, fmt.Errorf("resolve driver install lock location: %w", err)
	}
	// Nothing can be installed if the locations do not exist; do not create
	// them just to take locks.
	for _, path := range []string{location, lockLocation} {
		if _, statErr := os.Stat(path); statErr != nil {
			return DriverInfo{}, false, nil
		}
	}

	lock, err := acquireDriverInstallLockWith(ctx, lockLocation, runtimeID, 10*time.Second)
	if err != nil {
		if !readOnlyLockOpenError(err) {
			return DriverInfo{}, false, fmt.Errorf("acquire driver install lock: %w", err)
		}
	}
	optimistic := err != nil
	defer func() { err = errors.Join(err, lock.release()) }()
	namespacePath, registrationLocation, err := registrationNamespaceLockPath(cfg, lockLocation)
	if err != nil {
		return DriverInfo{}, false, err
	}
	if !optimistic {
		namespaceLock, lockErr := acquireDriverInstallLock(ctx, namespacePath, 10*time.Second)
		if lockErr != nil {
			if !readOnlyLockOpenError(lockErr) {
				return DriverInfo{}, false, fmt.Errorf("acquire registration namespace lock: %w", lockErr)
			}
			optimistic = true
		} else {
			defer func() { err = errors.Join(err, namespaceLock.release()) }()
		}
	}
	if err := ctx.Err(); err != nil {
		return DriverInfo{}, false, err
	}

	registered, ok := readPrimaryRuntimeRegistration(cfg, registrationLocation, runtimeID)
	if !ok || !installedPackageMatches(cfg, location, registrationLocation, runtimeID, registered, expect, optimistic) {
		if err := ctx.Err(); err != nil {
			return DriverInfo{}, false, err
		}
		return DriverInfo{}, false, nil
	}
	if err := ctx.Err(); err != nil {
		return DriverInfo{}, false, err
	}
	return registered, true, nil
}

// Only failure to open a writable lock permits optimistic reuse. Contention,
// cancellation, and lock-path validation failures retain their normal errors.
func readOnlyLockOpenError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, fslock.ErrLockContended) {
		return false
	}
	var pathErr *os.PathError
	return errors.As(err, &pathErr) && pathErr.Op == "open" &&
		(errors.Is(pathErr, fs.ErrPermission) || errors.Is(pathErr, syscall.EROFS))
}

// installedPackageMatches applies the strict reuse conditions to a
// registration read under the driver and namespace locks.
func installedPackageMatches(cfg Config, root, registrationLocation, runtimeID string, registered DriverInfo, expect InstalledPackageExpectation, optimistic bool) bool {
	if !sameRuntimeID(registered.ID, runtimeID) || registered.Source != "dbc" || !sameVersion(registered.Version, expect.Version) {
		return false
	}
	scope, err := packageRegistrationScopeForConfig(cfg)
	if err != nil {
		return false
	}
	shared := registered.Driver.Shared.Get(PlatformTuple())
	if shared == "" || hasParentTraversal(shared) {
		return false
	}
	// The generation is the directory holding the registered library. It must
	// be a direct child of the root; receiptMatchesRegistration below confirms
	// that the registered path really is that generation's owned library.
	generation := hostpath.Base(hostpath.Dir(resolvePackagePath(root, shared)))
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return false
	}
	defer rootHandle.Close()
	generationRoot, ok := openVerifiedGeneration(rootHandle, generation, root, packageCleanupOperations{})
	if !ok {
		return false
	}
	defer generationRoot.Close()
	receipt, ok := readPackageInstallReceiptAtRoot(generationRoot, generation)
	if !ok || !sameRuntimeID(receipt.RuntimeID, runtimeID) || receipt.Platform != PlatformTuple() || receipt.RegistrationScope != scope {
		return false
	}
	if !receiptMatchesRegistration(cfg, root, hostpath.Join(root, generation), receipt, registered) {
		return false
	}
	if receipt.ArchiveSHA256 == "" || receipt.ArchiveSHA256 != expect.ArchiveSHA256 {
		return false
	}
	if expect.SignatureVerifier != nil && !receipt.SignatureVerified {
		return false
	}
	if expect.LibrarySHA256 != "" && receipt.OwnedLibrarySHA256 != expect.LibrarySHA256 {
		return false
	}
	// The integrity check tolerates a removed library for cleanup; reuse needs
	// the registered library to be present.
	if _, err := generationRoot.Lstat(receipt.OwnedLibraryFilename); err != nil {
		return false
	}
	if verifyPackageOwnedLibraryIntegrityAtRoot(generationRoot, receipt, nil) != nil {
		return false
	}
	if expect.SignatureVerifier != nil {
		if receipt.SignatureFilename == "" || validatePackageFilename(receipt.SignatureFilename) != nil || receipt.SignatureFilename == receipt.OwnedLibraryFilename {
			return false
		}
		if !verifyInstalledPackageSignature(generationRoot, receipt, registered, expect.SignatureVerifier, optimistic) {
			return false
		}
	}
	if optimistic {
		// The handles pin the observed directories, not the runtime paths. A
		// writer may have replaced the registration or unlinked the generation
		// while the private signature copy was being checked.
		openedRoot, rootErr := rootHandle.Stat(".")
		currentRoot, currentRootErr := os.Stat(root)
		openedGeneration, generationErr := generationRoot.Stat(".")
		currentGeneration, currentGenerationErr := rootHandle.Lstat(generation)
		if rootErr != nil || currentRootErr != nil || !os.SameFile(openedRoot, currentRoot) ||
			generationErr != nil || currentGenerationErr != nil || !currentGeneration.IsDir() ||
			!os.SameFile(openedGeneration, currentGeneration) {
			return false
		}
		currentReceipt, ok := readPackageInstallReceiptAtRoot(generationRoot, generation)
		if !ok || currentReceipt != receipt {
			return false
		}
		library, ok := openVerifiedGenerationFile(generationRoot, receipt.OwnedLibraryFilename)
		if !ok {
			return false
		}
		digest, hashErr := hashSHA256(library)
		closeErr := library.Close()
		if hashErr != nil || closeErr != nil || hex.EncodeToString(digest) != receipt.OwnedLibrarySHA256 {
			return false
		}
		currentRegistration, ok := readPrimaryRuntimeRegistration(cfg, registrationLocation, runtimeID)
		if !ok || !sameDriverRegistration(registered, currentRegistration) ||
			!receiptMatchesRegistration(cfg, root, hostpath.Join(root, generation), receipt, currentRegistration) {
			return false
		}
	}
	return true
}

// verifyInstalledPackageSignature copies files opened relative to the pinned
// generation into a private directory before calling the path-based verifier.
// In optimistic mode the original signature is checked again after the callback
// and the caller rechecks the library and its registration. Otherwise the
// driver and namespace locks are held.
func verifyInstalledPackageSignature(generation *os.Root, receipt packageInstallReceipt, registered DriverInfo, verifier PackageVerifier, optimistic bool) bool {
	tempDir, err := os.MkdirTemp("", "dbc-package-verify-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(tempDir)
	files := []string{receipt.OwnedLibraryFilename, receipt.SignatureFilename}
	var signatureDigest []byte
	for index, name := range files {
		digest, ok := copyVerifiedGenerationFile(generation, name, hostpath.Join(tempDir, name))
		if !ok {
			return false
		}
		if index == 0 && hex.EncodeToString(digest) != receipt.OwnedLibrarySHA256 {
			return false
		}
		if index == 1 {
			signatureDigest = digest
		}
	}
	manifest := Manifest{DriverInfo: registered}
	manifest.Files.Driver = receipt.OwnedLibraryFilename
	manifest.Files.Signature = receipt.SignatureFilename
	manifest.Driver.Shared = driverMap{}
	manifest.Driver.Shared.Set(PlatformTuple(), hostpath.Join(tempDir, receipt.OwnedLibraryFilename))
	manifest.FilePath = hostpath.Join(tempDir, receipt.OwnedLibraryFilename)
	if verifier(tempDir, manifest) != nil {
		return false
	}
	if optimistic {
		f, ok := openVerifiedGenerationFile(generation, receipt.SignatureFilename)
		if !ok {
			return false
		}
		digest, hashErr := hashSHA256(f)
		closeErr := f.Close()
		if hashErr != nil || closeErr != nil || !bytes.Equal(digest, signatureDigest) {
			return false
		}
	}
	return true
}

func copyVerifiedGenerationFile(generation *os.Root, name, destination string) ([]byte, bool) {
	source, ok := openVerifiedGenerationFile(generation, name)
	if !ok {
		return nil, false
	}
	target, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = source.Close()
		return nil, false
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(target, hash), source)
	closeTargetErr := target.Close()
	closeSourceErr := source.Close()
	if copyErr != nil || closeTargetErr != nil || closeSourceErr != nil {
		return nil, false
	}
	return hash.Sum(nil), true
}

func openVerifiedGenerationFile(generation *os.Root, name string) (*os.File, bool) {
	info, err := generation.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	source, err := generation.Open(name)
	if err != nil {
		return nil, false
	}
	openedInfo, statErr := source.Stat()
	if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		_ = source.Close()
		return nil, false
	}
	return source, true
}
