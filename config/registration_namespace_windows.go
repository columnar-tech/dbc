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

func registrationNamespaceLockSpec(cfg Config, location string) (identity, lockDirectory, registrationLocation string, err error) {
	switch cfg.Level {
	case ConfigEnv:
		return fileRegistrationNamespaceLockSpec(location)
	case ConfigUser:
		return "registry-user:HKCU\\SOFTWARE\\ADBC\\Drivers", ConfigUser.ConfigLocation(), "HKCU\\SOFTWARE\\ADBC\\Drivers", nil
	case ConfigSystem:
		return "registry-system:HKLM\\SOFTWARE\\ADBC\\Drivers", ConfigSystem.ConfigLocation(), "HKLM\\SOFTWARE\\ADBC\\Drivers", nil
	default:
		return "", "", "", fmt.Errorf("unsupported registration config level %d", cfg.Level)
	}
}

func fileRegistrationNamespaceLockSpec(location string) (identity, lockDirectory, registrationLocation string, err error) {
	if location == "" {
		return "", "", "", fmt.Errorf("registration namespace location is empty")
	}
	absolute, err := filepath.Abs(location)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve registration namespace location: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(absolute))
	if err != nil {
		return "", "", "", fmt.Errorf("resolve registration namespace symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", "", fmt.Errorf("inspect registration namespace: %w", err)
	}
	if !info.IsDir() {
		return "", "", "", fmt.Errorf("registration namespace %s is not a directory", resolved)
	}
	resolved = filepath.Clean(resolved)
	return "file:" + strings.ToLower(resolved), resolved, resolved, nil
}

func collectRegistrationSharedMaps(cfg Config, location, excludedID string) ([]driverMap, bool, error) {
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
		shared = append(shared, registration.Driver.Shared)
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
