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

	"github.com/columnar-tech/dbc/internal/hostpath"
)

func fileRegistrationNamespaceIdentity(path string, windows bool) string {
	if windows {
		path = strings.ToLower(path)
	}
	return "file:" + path
}

func registrationNamespaceLockFilename(identity string) string {
	if strings.HasPrefix(identity, "registry-") {
		key := sha256.Sum256([]byte(identity))
		return ".dbc.namespace." + hex.EncodeToString(key[:]) + ".lock"
	}
	return ".dbc.namespace.lock"
}

// acquireRegistrationNamespaceLock is always called after the driver lock.
// The lock directory and identity are platform-specific because Windows
// registry scopes share registrations independently of package payload roots.
func acquireRegistrationNamespaceLock(ctx context.Context, cfg Config, location string, timeout time.Duration) (*driverInstallLock, string, error) {
	identity, lockDirectory, registrationLocation, err := registrationNamespaceLockSpec(cfg, location)
	if err != nil {
		return nil, "", err
	}
	canonicalDirectory, err := hostpath.Abs(lockDirectory)
	if err != nil {
		return nil, "", fmt.Errorf("resolve registration namespace lock directory: %w", err)
	}
	canonicalDirectory = hostpath.Clean(canonicalDirectory)
	if identity == "" {
		return nil, "", errors.New("registration namespace identity is empty")
	}
	// The root-local fixed name lets the filesystem resolve case and Unicode
	// aliases according to its own identity rules. Older pre-merge PR snapshots
	// used a root-hashed name and do not share this lock protocol.
	lockPath := hostpath.Join(canonicalDirectory, registrationNamespaceLockFilename(identity))
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
	return collectFileRegistrationSharedMapsExcluding(location, "", excludedID)
}

func collectFileRegistrationSharedMapsExcluding(location, excludedRoot, excludedID string) ([]driverMap, bool, error) {
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
		if sameRuntimeID(id, excludedID) && (excludedRoot == "" || sameResolvedFilesystemPath(location, excludedRoot)) {
			continue
		}
		path := hostpath.Join(location, name)
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
		resolvedShared, err := sharedPathsFromRegistrationRoot(location, manifest.DriverInfo.Driver.Shared)
		if err != nil {
			return nil, false, fmt.Errorf("resolve shared paths in registration %s: %w", path, err)
		}
		shared = append(shared, resolvedShared)
	}
	return shared, true, nil
}

func collectRegistrationSharedMaps(cfg Config, registrationLocation, excludedID string) ([]driverMap, bool, error) {
	return collectRegistrationSharedMapsExcluding(cfg, registrationLocation, "", excludedID)
}

func collectRegistrationSharedMapsExcluding(cfg Config, registrationLocation, excludedRoot, excludedID string) ([]driverMap, bool, error) {
	if cfg.Level != ConfigEnv {
		return collectScopedRegistrationSharedMaps(cfg, registrationLocation, excludedID)
	}

	// ConfigEnv can select a registration from any path in ADBC_DRIVER_PATH.
	// Scan every configured root without taking secondary locks or writing to
	// those roots. A missing root is empty; any other incomplete scan makes the
	// full reference set uncertain so cleanup can retain the payload.
	// This is a read-only scan, not a consistent multi-root snapshot; a
	// concurrent reference created in an unlocked secondary root can race cleanup.
	roots := configuredRegistrationRoots(cfg.Location, registrationLocation)
	var shared []driverMap
	for _, root := range roots {
		maps, certain, err := collectFileRegistrationSharedMapsExcluding(root, excludedRoot, excludedID)
		if err != nil || !certain {
			return nil, false, err
		}
		shared = append(shared, maps...)
	}
	return shared, true, nil
}

func configuredRegistrationRoots(configured, registrationLocation string) []string {
	var roots []string
	seen := make(map[string]struct{})
	for _, candidate := range append(splitConfigList(configured), registrationLocation) {
		if candidate == "" {
			continue
		}
		canonical, err := hostpath.Abs(hostpath.Clean(candidate))
		if err != nil {
			canonical = hostpath.Clean(candidate)
		}
		if resolved, err := hostpath.EvalSymlinks(canonical); err == nil {
			canonical = hostpath.Clean(resolved)
		}
		if hostpath.IsWindows() {
			canonical = strings.ToLower(canonical)
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		roots = append(roots, candidate)
	}
	return roots
}

func sharedPathsFromRegistrationRoot(root string, shared driverMap) (driverMap, error) {
	resolved := driverMap{defaultPath: shared.defaultPath}
	if resolved.defaultPath != "" {
		path, err := resolveRegistrationSharedPath(root, resolved.defaultPath)
		if err != nil {
			return driverMap{}, err
		}
		resolved.defaultPath = path
	}
	if len(shared.platformMap) > 0 {
		resolved.platformMap = make(map[string]string, len(shared.platformMap))
		for platform, sharedPath := range shared.platformMap {
			path, err := resolveRegistrationSharedPath(root, sharedPath)
			if err != nil {
				return driverMap{}, err
			}
			resolved.platformMap[platform] = path
		}
	}
	return resolved, nil
}

func resolveRegistrationSharedPath(root, sharedPath string) (string, error) {
	if sharedPath == "" {
		return "", nil
	}
	if hasParentTraversal(sharedPath) {
		return "", errors.New("shared path contains parent traversal")
	}
	if !hostpath.IsAbs(sharedPath) {
		sharedPath = hostpath.Join(root, sharedPath)
	}
	resolved, err := hostpath.Abs(hostpath.Clean(sharedPath))
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func readFileRuntimeRegistration(location, runtimeID string) (DriverInfo, bool) {
	if validatePackageFilename(runtimeID) != nil {
		return DriverInfo{}, false
	}
	path := hostpath.Join(location, runtimeID+".toml")
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
