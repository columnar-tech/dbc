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

//go:build !windows

package config

import (
	"fmt"
	"os"

	"github.com/columnar-tech/dbc/internal/hostpath"
)

func uninstallLockLocation(_ Config, info DriverInfo) (string, error) {
	if info.FilePath == "" {
		return "", fmt.Errorf("driver %q has no installation location", info.ID)
	}
	return info.FilePath, nil
}

func prepareDriverUninstallLockLocation(Config, string) error {
	return nil
}

func packageCleanupRoot(_ Config, info DriverInfo) (string, error) {
	if info.FilePath == "" {
		return "", nil
	}
	return info.FilePath, nil
}

func readDriverRegistrationForUninstall(_ Config, info DriverInfo) (DriverInfo, error) {
	return loadDriverFromManifest(info.FilePath, info.ID)
}

func removeDriverRegistration(cfg Config, info DriverInfo, operations packageCleanupOperations) error {
	if operations.removeRegistration != nil {
		return operations.removeRegistration(cfg, info)
	}
	manifest := hostpath.Join(info.FilePath, info.ID+".toml")
	if err := os.Remove(manifest); err != nil {
		return fmt.Errorf("error removing manifest %s: %w", manifest, err)
	}
	// Remove the symlink created during installation (one level up from the
	// manifest)
	// TODO: Remove this when the driver managers are fixed (>=1.8.1).
	removeManifestSymlink(info.FilePath, info.ID)
	return nil
}

func packageRegistrationScopeForConfig(Config) (packageRegistrationScope, error) {
	return packageRegistrationFile, nil
}

func createRuntimeRegistrationUnlocked(_ Config, location string, driver DriverInfo) error {
	return createDriverManifestUnlocked(location, driver)
}

func packageInstallLockLocation(_ Config, payloadLocation string) (string, error) {
	return payloadLocation, nil
}

func preparePackageInstallLockLocation(Config, string) error {
	return nil
}

func registrationNamespaceLockSpec(_ Config, location string) (identity, lockDirectory, registrationLocation string, err error) {
	return fileRegistrationNamespaceLockSpec(location)
}

func collectScopedRegistrationSharedMaps(_ Config, location, excludedID string) ([]driverMap, bool, error) {
	return collectFileRegistrationSharedMaps(location, excludedID)
}

func readPrimaryRuntimeRegistration(_ Config, registrationLocation, runtimeID string) (DriverInfo, bool) {
	return readFileRuntimeRegistration(registrationLocation, runtimeID)
}

func registrationFingerprintIncludesADBC(Config) bool { return true }
