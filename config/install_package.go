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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// PackageVerifier validates a fully extracted package before it is published.
// It runs while the runtime ID's driver lock is held and must not call a driver
// mutation API for that runtime ID. The manifest points to the planned final
// generation path while stagingDir contains the candidate package files.
type PackageVerifier func(stagingDir string, manifest Manifest) error

// InstallPackageOptions controls optional package verification.
type InstallPackageOptions struct {
	// Verifier runs under the driver lock before generation publication.
	Verifier PackageVerifier
}

type packageInstallOperations struct {
	rename       func(string, string) error
	remove       func(string) error
	removeAll    func(string) error
	register     func(Config, string, DriverInfo) error
	writeReceipt func(string, packageInstallReceipt) error
}

// InstallPackage installs a package generation and then updates the runtime
// registration to point to it. The archive file is closed on every return.
// On Unix, atomic replacement of the registration is the commit point. On
// Windows, registry values are updated with rollback after a partial failure,
// but a rollback failure or process termination during that update can leave
// the previous registration uncertain. If rollback fails, the candidate
// generation is preserved. After successful registration, stale receipt-backed
// generations are removed on a best-effort basis. An error while releasing the
// driver lock can be returned after commit; in that case the new generation
// remains installed. This API does not promise durability across power loss.
func InstallPackage(cfg Config, runtimeID string, downloaded *os.File, options InstallPackageOptions) (manifest Manifest, err error) {
	return installPackageWithOperations(cfg, runtimeID, downloaded, options, packageInstallOperations{
		rename:       os.Rename,
		remove:       os.Remove,
		removeAll:    os.RemoveAll,
		register:     createRuntimeRegistrationUnlocked,
		writeReceipt: writePackageInstallReceipt,
	})
}

func installPackageWithOperations(cfg Config, runtimeID string, downloaded *os.File, options InstallPackageOptions, operations packageInstallOperations) (manifest Manifest, err error) {
	if downloaded == nil {
		return Manifest{}, errors.New("downloaded package file is nil")
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, downloaded.Close())
		}
	}()
	if err := validatePackageFilename(runtimeID); err != nil {
		return Manifest{}, fmt.Errorf("invalid runtime ID: %w", err)
	}

	location, err := EnsureLocation(cfg)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not ensure config location: %w", err)
	}
	location, err = filepath.Abs(location)
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve config location: %w", err)
	}
	location = filepath.Clean(location)
	lockLocation, err := packageInstallLockLocation(cfg, location)
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve driver install lock location: %w", err)
	}
	if err := preparePackageInstallLockLocation(cfg, lockLocation); err != nil {
		return Manifest{}, fmt.Errorf("prepare driver install lock location: %w", err)
	}
	lock, err := acquireDriverInstallLockWith(context.Background(), lockLocation, runtimeID, 10*time.Second)
	if err != nil {
		return Manifest{}, fmt.Errorf("acquire driver install lock: %w", err)
	}
	defer func() { err = errors.Join(err, lock.release()) }()

	stageDir, manifest, _, err := extractPackageArchive(downloaded, location)
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to extract package: %w", err)
	}
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

	manifest.DriverInfo.ID = runtimeID
	manifest.DriverInfo.Source = "dbc"
	if manifest.Files.Driver == "" && manifest.Driver.Shared.Get(PlatformTuple()) == "" {
		return Manifest{}, fmt.Errorf("manifest-only package has no shared library for platform %s", PlatformTuple())
	}
	reservation, err := os.MkdirTemp(location, ".dbc-package-"+runtimeID+"-")
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
	if err := operations.remove(reservation); err != nil {
		return Manifest{}, fmt.Errorf("release package generation reservation: %w", err)
	}
	reservationExists = false
	if manifest.Files.Driver != "" {
		manifest.Driver.Shared = driverMap{}
		manifest.Driver.Shared.Set(PlatformTuple(), filepath.Join(generationDir, manifest.Files.Driver))
	}
	if options.Verifier != nil {
		if err := options.Verifier(stageDir, manifest); err != nil {
			return Manifest{}, fmt.Errorf("verify package: %w", err)
		}
	}
	namespaceLock, registrationLocation, err := acquireRegistrationNamespaceLock(context.Background(), cfg, lockLocation, 10*time.Second)
	if err != nil {
		return Manifest{}, err
	}
	defer func() { err = errors.Join(err, namespaceLock.release()) }()
	receipt, err := makePackageInstallReceipt(cfg, stageDir, filepath.Base(generationDir), runtimeID, PlatformTuple(), manifest)
	if err != nil {
		return Manifest{}, fmt.Errorf("prepare package install receipt: %w", err)
	}
	if err := operations.writeReceipt(stageDir, receipt); err != nil {
		return Manifest{}, fmt.Errorf("write package install receipt: %w", err)
	}

	if err := os.Chmod(stageDir, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("prepare package generation: %w", err)
	}
	if err := operations.rename(stageDir, generationDir); err != nil {
		return Manifest{}, fmt.Errorf("publish package generation: %w", err)
	}
	stageDir = ""
	if manifest.Files.Driver != "" {
		manifest.Driver.Shared = driverMap{}
		manifest.Driver.Shared.Set(PlatformTuple(), filepath.Join(generationDir, manifest.Files.Driver))
	}

	if err := operations.register(cfg, location, manifest.DriverInfo); err != nil {
		return Manifest{}, cleanupFailedPackageRegistration(generationDir, err, operations.removeAll)
	}
	registrations, certain, _ := collectRegistrationSharedMaps(cfg, registrationLocation, "")
	if certain {
		referenced, uncertain := referencedPathsForRegistrations(location, registrations)
		cleanupStalePackageGenerationsWithReferences(cfg, location, manifest.DriverInfo, []string{generationDir}, referenced, uncertain, operations.remove, operations.removeAll)
	}
	return manifest, nil
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
