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
	if strings.Contains(info.FilePath, "HKCU\\") {
		return ConfigUser.ConfigLocation(), nil
	}
	if strings.Contains(info.FilePath, "HKLM\\") {
		return ConfigSystem.ConfigLocation(), nil
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

func uninstallDriverUnlocked(cfg Config, info DriverInfo) error {
	registrations, certain, _ := collectRegistrationSharedMapsExcluding(cfg, info.FilePath, info.FilePath, info.ID)
	return uninstallDriverUnlockedWithReferences(cfg, info, registrations, certain)
}

func uninstallDriverUnlockedWithCleanup(cfg Config, info DriverInfo, operations packageCleanupOperations) error {
	registrations, certain, _ := collectRegistrationSharedMapsExcluding(cfg, info.FilePath, info.FilePath, info.ID)
	return uninstallDriverUnlockedWithCleanupAndReferences(cfg, info, registrations, certain, operations)
}

func uninstallDriverUnlockedWithReferences(cfg Config, info DriverInfo, otherRegistrations []driverMap, referencesCertain bool) error {
	return uninstallDriverUnlockedWithCleanupAndReferences(cfg, info, otherRegistrations, referencesCertain, packageCleanupOperations{})
}

func uninstallDriverUnlockedWithCleanupAndReferences(cfg Config, info DriverInfo, otherRegistrations []driverMap, referencesCertain bool, operations packageCleanupOperations) error {
	if info.Source == "dbc" {
		root, err := packageCleanupRoot(packageCleanupConfig(cfg, info), info)
		if err != nil {
			return err
		}
		if err := removeDriverRegistration(cfg, info, operations); err != nil {
			return err
		}
		if err := cleanupInstalledPackageWithReferences(cfg, root, info, otherRegistrations, referencesCertain, operations); err != nil {
			return fmt.Errorf("driver registration was removed, but package cleanup failed; package files may remain under %s: %w", root, err)
		}
		return nil
	}
	if err := uninstallDriverSharedWithReferences(cfg, info, operations, otherRegistrations, referencesCertain); err != nil {
		return fmt.Errorf("failed to delete driver shared object: %w", err)
	}
	return removeDriverRegistration(cfg, info, operations)
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
