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
	"os"
	"path/filepath"
	"strings"
	"time"
)

// acquireRegistrationNamespaceLock is always called after the driver lock.
// The lock directory and identity are platform-specific because Windows
// registry scopes share registrations independently of package payload roots.
func acquireRegistrationNamespaceLock(ctx context.Context, cfg Config, location string, timeout time.Duration) (*driverInstallLock, string, error) {
	identity, lockDirectory, registrationLocation, err := registrationNamespaceLockSpec(cfg, location)
	if err != nil {
		return nil, "", err
	}
	canonicalDirectory, err := filepath.Abs(lockDirectory)
	if err != nil {
		return nil, "", fmt.Errorf("resolve registration namespace lock directory: %w", err)
	}
	canonicalDirectory = filepath.Clean(canonicalDirectory)
	if identity == "" {
		return nil, "", errors.New("registration namespace identity is empty")
	}
	key := sha256.Sum256([]byte(identity))
	lockPath := filepath.Join(canonicalDirectory, ".dbc.namespace."+hex.EncodeToString(key[:])+".lock")
	lock, err := acquireDriverInstallLock(ctx, lockPath, timeout)
	if err != nil {
		return nil, "", fmt.Errorf("acquire registration namespace lock: %w", err)
	}
	return lock, registrationLocation, nil
}

// collectFileRegistrationSharedMaps returns complete=false if any registration
// cannot be read with confidence. Callers must skip package cleanup in that
// case while continuing the requested registration mutation.
func collectFileRegistrationSharedMaps(location, excludedID string) ([]driverMap, bool, error) {
	entries, err := readDirectoryEntries(location)
	if errors.Is(err, os.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read registration namespace %s: %w", location, err)
	}
	var shared []driverMap
	for _, entry := range entries {
		name := entry.Name()
		extension := filepath.Ext(name)
		if !strings.EqualFold(extension, ".toml") {
			continue
		}
		id := strings.TrimSuffix(name, extension)
		if sameRuntimeID(id, excludedID) {
			continue
		}
		path := filepath.Join(location, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			if err == nil {
				err = fmt.Errorf("registration is not a regular file")
			}
			return nil, false, fmt.Errorf("inspect registration %s: %w", path, err)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, false, fmt.Errorf("open registration %s: %w", path, err)
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
			closeErr := file.Close()
			if statErr == nil {
				statErr = errors.New("registration changed while being read")
			}
			return nil, false, errors.Join(fmt.Errorf("verify registration %s: %w", path, statErr), closeErr)
		}
		manifest, decodeErr := decodeManifest(file, id, true)
		closeErr := file.Close()
		if decodeErr != nil || closeErr != nil || manifest.DriverInfo.ID != id {
			cause := errors.Join(decodeErr, closeErr)
			if cause == nil {
				cause = errors.New("registration identity does not match its filename")
			}
			return nil, false, fmt.Errorf("decode registration %s: %w", path, cause)
		}
		shared = append(shared, manifest.DriverInfo.Driver.Shared)
	}
	return shared, true, nil
}

func readFileRuntimeRegistration(location, runtimeID string) (DriverInfo, bool) {
	if validatePackageFilename(runtimeID) != nil {
		return DriverInfo{}, false
	}
	path := filepath.Join(location, runtimeID+".toml")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return DriverInfo{}, false
	}
	file, err := os.Open(path)
	if err != nil {
		return DriverInfo{}, false
	}
	openedInfo, statErr := file.Stat()
	if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return DriverInfo{}, false
	}
	manifest, decodeErr := decodeManifest(file, runtimeID, true)
	closeErr := file.Close()
	if decodeErr != nil || closeErr != nil || manifest.DriverInfo.ID != runtimeID {
		return DriverInfo{}, false
	}
	manifest.DriverInfo.FilePath = location
	return manifest.DriverInfo, true
}
