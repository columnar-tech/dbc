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
	"io"
	"os"
	"time"

	"github.com/columnar-tech/dbc/internal/hostpath"
)

// PackageVerifier validates extracted package files. During installation it
// runs under the runtime ID's driver lock before publication; installed reuse
// may instead verify a read-only snapshot (see InstalledPackageExpectation).
// It must not call a driver mutation API for that runtime ID. During installation
// the manifest points to the planned final generation while stagingDir contains
// the candidate package files.
type PackageVerifier func(stagingDir string, manifest Manifest) error

// InstallPackageOptions controls optional package verification.
type InstallPackageOptions struct {
	// ExpectedArchiveSHA256 is the optional lowercase hex SHA-256 (64
	// characters) the caller requires the downloaded archive to have. A
	// different measured digest fails the install with an
	// *ArchiveHashMismatchError before anything is published.
	ExpectedArchiveSHA256 string
	// Verifier runs under the driver lock before generation publication.
	Verifier PackageVerifier
	// SignatureVerifier runs on the staged package right after Verifier and
	// under the same conditions. Only its success marks the install as
	// signature-verified in the install receipt. The receipt records only that
	// this caller-supplied callback succeeded; config does not guarantee the
	// verification method, key, source trust, or provenance.
	SignatureVerifier PackageVerifier
}

// ArchiveHashMismatchError reports that the SHA-256 of the downloaded archive
// differs from the digest the caller required.
type ArchiveHashMismatchError struct {
	// Expected is the digest the caller required and Actual the measured one;
	// both are lowercase hex SHA-256 digests.
	Expected string
	Actual   string
}

func (e *ArchiveHashMismatchError) Error() string {
	return fmt.Sprintf("archive SHA-256 mismatch: expected %s, got %s", e.Expected, e.Actual)
}

func validArchiveSHA256(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

type packageInstallOperations struct {
	rename              func(string, string) error
	remove              func(string) error
	removeAll           func(string) error
	cleanupBeforeRemove func(string) error
	register            func(Config, string, DriverInfo) error
	writeReceipt        func(string, packageInstallReceipt) error
}

// InstallPackage installs a package generation, then points the runtime
// registration at it, and closes the archive file on every return. Stale
// generations are removed best-effort afterwards. A lock release error can be
// returned after commit; the new generation then remains installed.
// On Windows, crash atomicity of the registry update is not guaranteed.
// Cancellation is checked only at transaction boundaries; once registration
// starts it completes.
func InstallPackage(ctx context.Context, cfg Config, runtimeID string, downloaded *os.File, options InstallPackageOptions) (manifest Manifest, err error) {
	return installPackageWithContextAndOperations(ctx, cfg, runtimeID, downloaded, options, packageInstallOperations{
		rename:       os.Rename,
		remove:       os.Remove,
		removeAll:    os.RemoveAll,
		register:     createRuntimeRegistrationUnlocked,
		writeReceipt: writePackageInstallReceipt,
	})
}

func installPackageWithOperations(cfg Config, runtimeID string, downloaded *os.File, options InstallPackageOptions, operations packageInstallOperations) (manifest Manifest, err error) {
	return installPackageWithContextAndOperations(context.Background(), cfg, runtimeID, downloaded, options, operations)
}

func installPackageWithContextAndOperations(ctx context.Context, cfg Config, runtimeID string, downloaded *os.File, options InstallPackageOptions, operations packageInstallOperations) (manifest Manifest, err error) {
	if downloaded == nil {
		return Manifest{}, errors.New("downloaded package file is nil")
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, downloaded.Close())
		}
	}()
	if options.ExpectedArchiveSHA256 != "" && !validArchiveSHA256(options.ExpectedArchiveSHA256) {
		return Manifest{}, fmt.Errorf("invalid expected archive SHA-256 %q: want 64 lowercase hex characters", options.ExpectedArchiveSHA256)
	}
	if ctx == nil {
		return Manifest{}, errors.New("install context is nil")
	}
	if err := validatePackageFilename(runtimeID); err != nil {
		return Manifest{}, fmt.Errorf("invalid runtime ID: %w", err)
	}

	location, err := EnsureLocation(cfg)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not ensure config location: %w", err)
	}
	location, err = hostpath.Abs(location)
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve config location: %w", err)
	}
	location = hostpath.Clean(location)
	lockLocation, err := packageInstallLockLocation(cfg, location)
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve driver install lock location: %w", err)
	}
	if err := preparePackageInstallLockLocation(cfg, lockLocation); err != nil {
		return Manifest{}, fmt.Errorf("prepare driver install lock location: %w", err)
	}
	lock, err := acquireDriverInstallLockWith(ctx, lockLocation, runtimeID, 10*time.Second)
	if err != nil {
		return Manifest{}, fmt.Errorf("acquire driver install lock: %w", err)
	}
	defer func() { err = errors.Join(err, lock.release()) }()
	if err := ctx.Err(); err != nil {
		return Manifest{}, fmt.Errorf("install canceled before staging: %w", err)
	}
	if options.ExpectedArchiveSHA256 != "" {
		if _, err := downloaded.Seek(0, io.SeekStart); err != nil {
			return Manifest{}, fmt.Errorf("seek archive before hash verification: %w", err)
		}
		sum, err := hashSHA256(downloaded)
		if err != nil {
			return Manifest{}, fmt.Errorf("hash archive before extraction: %w", err)
		}
		actual := hex.EncodeToString(sum)
		if actual != options.ExpectedArchiveSHA256 {
			return Manifest{}, &ArchiveHashMismatchError{Expected: options.ExpectedArchiveSHA256, Actual: actual}
		}
		if _, err := downloaded.Seek(0, io.SeekStart); err != nil {
			return Manifest{}, fmt.Errorf("rewind archive after hash verification: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return Manifest{}, fmt.Errorf("install canceled after archive hash verification: %w", err)
		}
	}

	// Extraction measures the same descriptor again, so a change after the
	// preflight check cannot be published under the expected archive digest.
	stageDir, manifest, _, archiveSHA256, err := extractPackageArchiveWithDigest(downloaded, location, defaultPackageArchiveLimits)
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to extract package: %w", err)
	}
	driverFilename := manifest.Files.Driver
	defer func() {
		if stageDir != "" {
			if cleanupErr := operations.removeAll(stageDir); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("remove staging directory %s: %w", stageDir, cleanupErr))
			}
		}
	}()
	if closeErr := downloaded.Close(); closeErr != nil {
		closed = true
		return Manifest{}, fmt.Errorf("close package archive: %w", closeErr)
	}
	closed = true

	if options.ExpectedArchiveSHA256 != "" && options.ExpectedArchiveSHA256 != archiveSHA256 {
		return Manifest{}, &ArchiveHashMismatchError{Expected: options.ExpectedArchiveSHA256, Actual: archiveSHA256}
	}

	manifest.DriverInfo.ID = runtimeID
	manifest.DriverInfo.Source = "dbc"
	generationPrefix, err := packageGenerationPrefix(runtimeID)
	if err != nil {
		return Manifest{}, fmt.Errorf("prepare package generation name: %w", err)
	}
	reservation, err := os.MkdirTemp(location, generationPrefix)
	if err != nil {
		return Manifest{}, fmt.Errorf("reserve package generation path: %w", err)
	}
	generationDir := reservation
	reservationExists := true
	defer func() {
		if reservationExists {
			if cleanupErr := operations.removeAll(reservation); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("remove package generation reservation %s: %w", reservation, cleanupErr))
			}
		}
	}()
	generationMode, err := umaskedDirMode(reservation, "mode", 0o755)
	if err != nil {
		return Manifest{}, fmt.Errorf("prepare package generation: %w", err)
	}
	if err := operations.remove(reservation); err != nil {
		return Manifest{}, fmt.Errorf("release package generation reservation: %w", err)
	}
	reservationExists = false
	manifest.Driver.Shared = driverMap{}
	manifest.Driver.Shared.Set(PlatformTuple(), hostpath.Join(generationDir, driverFilename))
	signatureVerified := false
	for _, step := range []struct {
		verify    PackageVerifier
		signature bool
	}{{options.Verifier, false}, {options.SignatureVerifier, true}} {
		if step.verify == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return Manifest{}, fmt.Errorf("install canceled before package verification: %w", err)
		}
		if err := step.verify(stageDir, manifest); err != nil {
			return Manifest{}, fmt.Errorf("verify package: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return Manifest{}, fmt.Errorf("install canceled after package verification: %w", err)
		}
		if step.signature {
			signatureVerified = true
		}
	}
	namespaceLock, registrationLocation, err := acquireRegistrationNamespaceLock(ctx, cfg, lockLocation, 10*time.Second)
	if err != nil {
		return Manifest{}, err
	}
	defer func() { err = errors.Join(err, namespaceLock.release()) }()
	legacyCandidate, hasLegacyCandidate := legacyPackageReplacementCandidate(cfg, location, registrationLocation, runtimeID)
	receipt, err := makePackageInstallReceipt(cfg, stageDir, hostpath.Base(generationDir), runtimeID, PlatformTuple(), manifest.DriverInfo, driverFilename)
	if err != nil {
		return Manifest{}, fmt.Errorf("prepare package install receipt: %w", err)
	}
	receipt.ArchiveSHA256 = archiveSHA256
	receipt.SignatureVerified = signatureVerified
	if signatureVerified {
		receipt.SignatureFilename = manifest.Files.Signature
		if receipt.SignatureFilename == "" {
			receipt.SignatureFilename = driverFilename + ".sig"
		}
	}
	if err := operations.writeReceipt(stageDir, receipt); err != nil {
		return Manifest{}, fmt.Errorf("write package install receipt: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return Manifest{}, fmt.Errorf("install canceled before publishing package generation: %w", err)
	}
	if err := os.Chmod(stageDir, generationMode); err != nil {
		return Manifest{}, fmt.Errorf("prepare package generation: %w", err)
	}
	if err := operations.rename(stageDir, generationDir); err != nil {
		return Manifest{}, fmt.Errorf("publish package generation: %w", err)
	}
	stageDir = ""

	if err := ctx.Err(); err != nil {
		return Manifest{}, cleanupFailedPackageRegistration(generationDir, fmt.Errorf("install canceled before registration: %w", err), operations.removeAll)
	}
	if err := operations.register(cfg, location, manifest.DriverInfo); err != nil {
		return Manifest{}, cleanupFailedPackageRegistration(generationDir, err, operations.removeAll)
	}
	registrations, certain, _ := collectRegistrationSharedMaps(cfg, registrationLocation, "")
	if certain {
		referenced, uncertain := referencedPathsForRegistrations(location, registrations)
		if hasLegacyCandidate {
			// Best-effort compatibility cleanup uses the registration captured
			// before commit and removes only its exact regular library file.
			_ = cleanupLegacyPackageRegistration(location, legacyCandidate, referenced, !uncertain, packageCleanupOperations{
				beforeRemove: operations.cleanupBeforeRemove,
			})
		}
		cleanupStalePackageGenerationsWithReferences(cfg, location, manifest.DriverInfo, []string{generationDir}, referenced, uncertain, packageCleanupOperations{
			beforeRemove: operations.cleanupBeforeRemove,
		})
	}
	return manifest, nil
}

// umaskedDirMode returns the mode os.Mkdir would give a directory created with
// perm, i.e. with the process umask applied. parent must be a unique private
// directory so the probe cannot collide with anything.
func umaskedDirMode(parent, name string, perm os.FileMode) (os.FileMode, error) {
	probe := hostpath.Join(parent, name)
	if err := os.Mkdir(probe, perm); err != nil {
		return 0, err
	}
	info, statErr := os.Stat(probe)
	removeErr := os.Remove(probe)
	if statErr != nil {
		return 0, statErr
	}
	return info.Mode().Perm(), removeErr
}

func cleanupFailedPackageRegistration(generationDir string, registrationErr error, removeAll func(string) error) error {
	if errors.Is(registrationErr, errRegistrationRollbackFailed) {
		return fmt.Errorf("register package failed; candidate generation preserved at %s: %w", generationDir, registrationErr)
	}
	if cleanupErr := removeAll(generationDir); cleanupErr != nil {
		return errors.Join(fmt.Errorf("register package: %w", registrationErr), fmt.Errorf("remove unpublished generation %s: %w", generationDir, cleanupErr))
	}
	return fmt.Errorf("register package: %w", registrationErr)
}
