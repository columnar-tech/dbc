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

//go:build windows

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func uninstallLockLocation(cfg Config, info DriverInfo) (string, error) {
	switch cfg.Level {
	case ConfigUser, ConfigSystem:
		return registrationLockLocation(cfg)
	case ConfigEnv:
		if info.FilePath != "" {
			return info.FilePath, nil
		}
		if cfg.Location != "" {
			return cfg.Location, nil
		}
		return "", fmt.Errorf("driver %q has no installation location", info.ID)
	default:
		return "", fmt.Errorf("driver %q has unsupported config level %d", info.ID, cfg.Level)
	}
}

func prepareDriverUninstallLockLocation(cfg Config, location string) error {
	return prepareRegistrationLockLocation(cfg, location)
}

func packageCleanupRoot(cfg Config, info DriverInfo) (string, error) {
	if cfg.Level == ConfigEnv {
		if info.FilePath == "" {
			return "", nil
		}
		return info.FilePath, nil
	}
	if cfg.Level == ConfigUser || cfg.Level == ConfigSystem {
		if cfg.Location != "" {
			return cfg.Location, nil
		}
		return cfg.Level.ConfigLocation(), nil
	}
	if info.FilePath == "" {
		return "", nil
	}
	return info.FilePath, nil
}

// Registry mutations are serialized with file locks under ConfigLocation.
// This coordinates cooperating processes only when they resolve the same lock
// directory. In particular, HKCU is shared even if processes have different
// APPDATA values, so this assumes a stable per-user ConfigLocation.
// TODO: Derive a stable lock identity from the HKCU user/hive so differing
// APPDATA values cannot split cooperating processes across lock directories.
func registrationLockLocation(cfg Config) (string, error) {
	if cfg.Level == ConfigEnv {
		if cfg.Location == "" {
			return "", fmt.Errorf("cannot write manifest to env config without %s set", adbcEnvVar)
		}
		return EnsureLocation(cfg)
	}
	if cfg.Level != ConfigUser && cfg.Level != ConfigSystem {
		return "", fmt.Errorf("unsupported config level %d", cfg.Level)
	}
	location := cfg.Level.ConfigLocation()
	if location == "" {
		return "", errors.New("driver registration location is empty")
	}
	return location, nil
}

func prepareRegistrationLockLocation(cfg Config, location string) error {
	if cfg.Level == ConfigEnv {
		return nil
	}
	return os.MkdirAll(location, 0o755)
}

func readDriverRegistrationForUninstall(cfg Config, info DriverInfo) (DriverInfo, error) {
	if cfg.Level != ConfigEnv {
		return GetDriver(cfg, info.ID)
	}
	return loadDriverFromManifest(info.FilePath, info.ID)
}

func removeDriverRegistration(cfg Config, info DriverInfo, operations packageCleanupOperations) error {
	if operations.removeRegistration != nil {
		return operations.removeRegistration(cfg, info)
	}
	if cfg.Level != ConfigEnv {
		k, err := registry.OpenKey(cfg.Level.key(), regKeyADBC, registry.ALL_ACCESS)
		if err != nil {
			return fmt.Errorf("open driver registry key for removal: %w", err)
		}
		deleteErr := registry.DeleteKey(k, info.ID)
		closeErr := k.Close()
		if deleteErr != nil {
			return fmt.Errorf("failed to delete driver registry key: %w", errors.Join(deleteErr, closeErr))
		}
		return nil
	}
	manifest := filepath.Join(info.FilePath, info.ID+".toml")
	if err := os.Remove(manifest); err != nil {
		return fmt.Errorf("error removing manifest %s: %w", manifest, err)
	}
	// TODO: Remove this when the driver managers are fixed (>=1.8.1).
	removeManifestSymlink(info.FilePath, info.ID)
	return nil
}

func packageRegistrationScopeForConfig(cfg Config) (packageRegistrationScope, error) {
	switch cfg.Level {
	case ConfigEnv:
		return packageRegistrationFile, nil
	case ConfigUser:
		return packageRegistrationRegistryUser, nil
	case ConfigSystem:
		return packageRegistrationRegistrySystem, nil
	default:
		return "", fmt.Errorf("unsupported config level %d", cfg.Level)
	}
}

func createRuntimeRegistrationUnlocked(cfg Config, location string, driver DriverInfo) error {
	if cfg.Level == ConfigEnv {
		return createDriverManifestUnlocked(location, driver)
	}
	return createRegistryManifestUnlocked(cfg, driver)
}

func packageInstallLockLocation(cfg Config, payloadLocation string) (string, error) {
	if cfg.Level == ConfigEnv {
		return payloadLocation, nil
	}
	location, err := registrationLockLocation(cfg)
	if err != nil {
		return "", fmt.Errorf("resolve registration lock location: %w", err)
	}
	return location, nil
}

func preparePackageInstallLockLocation(cfg Config, lockLocation string) error {
	if cfg.Level == ConfigEnv {
		return nil
	}
	return prepareRegistrationLockLocation(cfg, lockLocation)
}

func registrationNamespaceLockSpec(cfg Config, location string) (identity, lockDirectory, registrationLocation string, err error) {
	switch cfg.Level {
	case ConfigEnv:
		return fileRegistrationNamespaceLockSpec(location)
	case ConfigUser:
		// The lock file lives under ConfigLocation(), so processes with different
		// APPDATA values do not share this lock despite mutating the same registry.
		// TODO: Derive a stable HKCU lock identity independent of APPDATA.
		return "registry-user:HKCU\\SOFTWARE\\ADBC\\Drivers", ConfigUser.ConfigLocation(), "HKCU\\SOFTWARE\\ADBC\\Drivers", nil
	case ConfigSystem:
		return "registry-system:HKLM\\SOFTWARE\\ADBC\\Drivers", ConfigSystem.ConfigLocation(), "HKLM\\SOFTWARE\\ADBC\\Drivers", nil
	default:
		return "", "", "", fmt.Errorf("unsupported registration config level %d", cfg.Level)
	}
}

func collectScopedRegistrationSharedMaps(cfg Config, location, excludedID string) ([]driverMap, bool, error) {
	if cfg.Level == ConfigEnv {
		return collectFileRegistrationSharedMaps(location, excludedID)
	}
	if cfg.Level != ConfigUser && cfg.Level != ConfigSystem {
		return nil, false, fmt.Errorf("unsupported registration config level %d", cfg.Level)
	}
	root, err := registry.OpenKey(cfg.Level.key(), regKeyADBC, registry.READ)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open registration registry namespace: %w", err)
	}
	// Registry registrations do not carry a manifest directory. Resolve
	// relative shared paths against the config level's filesystem root, which
	// is the root used by external-library cleanup for that registry scope.
	filesystemRoot := cfg.Level.ConfigLocation()
	if filesystemRoot == "" {
		return nil, false, errors.Join(errors.New("registration filesystem root is empty"), root.Close())
	}
	info, err := root.Stat()
	if err != nil {
		return nil, false, errors.Join(fmt.Errorf("inspect registration registry namespace: %w", err), root.Close())
	}
	names, err := root.ReadSubKeyNames(int(info.SubKeyCount))
	if err != nil {
		return nil, false, errors.Join(fmt.Errorf("enumerate registration registry namespace: %w", err), root.Close())
	}
	var shared []driverMap
	for _, name := range names {
		if strings.EqualFold(name, excludedID) {
			continue
		}
		registration, err := driverInfoFromKey(root, name, cfg.Level)
		if err != nil {
			return nil, false, errors.Join(fmt.Errorf("read registration %q: %w", name, err), root.Close())
		}
		resolvedShared, err := sharedPathsFromRegistrationRoot(filesystemRoot, registration.Driver.Shared)
		if err != nil {
			return nil, false, errors.Join(fmt.Errorf("resolve shared paths in registration %q: %w", name, err), root.Close())
		}
		shared = append(shared, resolvedShared)
	}
	if err := root.Close(); err != nil {
		return nil, false, fmt.Errorf("close registration registry namespace: %w", err)
	}
	return shared, true, nil
}

func readPrimaryRuntimeRegistration(cfg Config, registrationLocation, runtimeID string) (DriverInfo, bool) {
	if cfg.Level == ConfigEnv {
		return readFileRuntimeRegistration(registrationLocation, runtimeID)
	}
	if cfg.Level != ConfigUser && cfg.Level != ConfigSystem {
		return DriverInfo{}, false
	}
	root, err := registry.OpenKey(cfg.Level.key(), regKeyADBC, registry.READ)
	if err != nil {
		return DriverInfo{}, false
	}
	registration, readErr := driverInfoFromKey(root, runtimeID, cfg.Level)
	closeErr := root.Close()
	if readErr != nil || closeErr != nil || registration.ID != runtimeID {
		return DriverInfo{}, false
	}
	return registration, true
}

func registrationFingerprintIncludesADBC(cfg Config) bool { return cfg.Level == ConfigEnv }
