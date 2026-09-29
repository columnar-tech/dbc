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
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

func uninstallLockLocation(cfg Config, info DriverInfo) (string, error) {
	if cfg.Level != ConfigEnv {
		location := cfg.Location
		if location == "" {
			location = cfg.Level.ConfigLocation()
		}
		if location == "" {
			return "", fmt.Errorf("driver %q has no installation location", info.ID)
		}
		return location, nil
	}
	if info.FilePath != "" {
		return info.FilePath, nil
	}
	if cfg.Location != "" {
		return cfg.Location, nil
	}
	return "", fmt.Errorf("driver %q has no installation location", info.ID)
}

func prepareDriverUninstallLockLocation(cfg Config, location string) error {
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
	if err := UninstallDriverShared(info); err != nil {
		return fmt.Errorf("failed to delete driver shared object: %w", err)
	}
	if cfg.Level != ConfigEnv {
		k, err := registry.OpenKey(cfg.Level.key(), regKeyADBC, registry.ALL_ACCESS)
		if err != nil {
			return err
		}
		defer k.Close()
		if err := registry.DeleteKey(k, info.ID); err != nil {
			return fmt.Errorf("failed to delete driver registry key: %w", err)
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
