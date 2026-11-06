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
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/internal/hostpath"
)

// InstalledPackageExpectation describes the package a caller wants to reuse.
type InstalledPackageExpectation struct {
	// Version is the driver version the registration must have. Required.
	Version *semver.Version
	// ArchiveSHA256 is the lowercase hex SHA-256 (64 characters) of the archive
	// the package must have been installed from. Required.
	ArchiveSHA256 string
	// RequireSignatureVerified rejects generations whose receipt does not
	// record a successful signature verifier.
	RequireSignatureVerified bool
}

// CheckInstalledPackage reports whether runtimeID's current registration at
// cfg's level is a dbc-owned package generation that matches expect: the
// registration, its install receipt, the installed library bytes and the
// recorded archive digest must all agree. Any mismatch, missing, corrupt or
// legacy state yields (DriverInfo{}, false, nil) so the caller can reinstall.
// Errors are returned only for invalid input, context cancellation and lock
// failures. The check never creates, modifies or removes anything and looks
// only at cfg's primary location. On platforms without pinned directory
// handles it always reports false.
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
		return DriverInfo{}, false, fmt.Errorf("acquire driver install lock: %w", err)
	}
	defer func() { err = errors.Join(err, lock.release()) }()
	namespaceLock, registrationLocation, err := acquireRegistrationNamespaceLock(ctx, cfg, lockLocation, 10*time.Second)
	if err != nil {
		return DriverInfo{}, false, err
	}
	defer func() { err = errors.Join(err, namespaceLock.release()) }()

	registered, ok := readPrimaryRuntimeRegistration(cfg, registrationLocation, runtimeID)
	if !ok || !installedPackageMatches(cfg, location, runtimeID, registered, expect) {
		return DriverInfo{}, false, nil
	}
	return registered, true, nil
}

// installedPackageMatches applies the strict reuse conditions to a
// registration read under the driver and namespace locks.
func installedPackageMatches(cfg Config, root, runtimeID string, registered DriverInfo, expect InstalledPackageExpectation) bool {
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
	if expect.RequireSignatureVerified && !receipt.SignatureVerified {
		return false
	}
	// The integrity check tolerates an already removed library for cleanup;
	// reuse needs the library to be present.
	if _, err := generationRoot.Lstat(receipt.OwnedLibraryFilename); err != nil {
		return false
	}
	return verifyPackageOwnedLibraryIntegrityAtRoot(generationRoot, receipt, nil) == nil
}
