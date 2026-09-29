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
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func registrationNamespaceLockSpec(_ Config, location string) (identity, lockDirectory, registrationLocation string, err error) {
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
	return "file:" + resolved, resolved, resolved, nil
}

func collectRegistrationSharedMaps(_ Config, location, excludedID string) ([]driverMap, bool, error) {
	return collectFileRegistrationSharedMaps(location, excludedID)
}

func packageCleanupRootForSharedHelper(cfg Config, info DriverInfo) (string, error) {
	return packageCleanupRoot(cfg, info)
}

func readDriverRegistrationForSharedCleanup(_ Config, info DriverInfo) (current DriverInfo, exists bool, err error) {
	if info.FilePath == "" {
		return DriverInfo{}, false, fmt.Errorf("driver %q has no registration location", info.ID)
	}
	path := filepath.Join(info.FilePath, info.ID+".toml")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return DriverInfo{}, false, nil
	}
	if err != nil {
		return DriverInfo{}, false, fmt.Errorf("open current registration %s: %w", path, err)
	}
	manifest, decodeErr := decodeManifest(file, info.ID, true)
	closeErr := file.Close()
	if decodeErr != nil || closeErr != nil {
		cause := errors.Join(decodeErr, closeErr)
		return DriverInfo{}, false, fmt.Errorf("read current registration %s: %w", path, cause)
	}
	manifest.DriverInfo.FilePath = info.FilePath
	return manifest.DriverInfo, true, nil
}
