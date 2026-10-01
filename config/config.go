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
	"io"
	"io/fs"
	"maps"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/columnar-tech/dbc/internal/hostpath"
	"github.com/pelletier/go-toml/v2"
)

const adbcEnvVar = "ADBC_DRIVER_PATH"

var platformTuple string

var ErrInvalidManifest = errors.New("invalid manifest")

func init() {
	os := runtime.GOOS
	switch os {
	case "darwin":
		os = "macos"
	case "windows", "freebsd", "linux", "openbsd":
	default:
		os = "unknown"
	}

	arch := runtime.GOARCH
	switch arch {
	case "386":
		arch = "x86"
	case "ppc":
		arch = "powerpc"
	case "ppc64":
		arch = "powerpc64"
	case "ppc64le":
		arch = "powerpc64le"
	case "wasm":
		arch = "wasm64"
	default:
	}

	platformTuple = os + "_" + arch
}

func PlatformTuple() string {
	return platformTuple
}

type Config struct {
	Level    ConfigLevel
	Location string
	Drivers  map[string]DriverInfo
	Exists   bool
	Err      error
}

type ConfigLevel int

const (
	ConfigUnknown ConfigLevel = iota
	ConfigSystem
	ConfigUser
	ConfigEnv
)

func (c ConfigLevel) String() string {
	switch c {
	case ConfigSystem:
		return "system"
	case ConfigUser:
		return "user"
	case ConfigEnv:
		return "env"
	default:
		return "unknown"
	}
}

var validLevelArgConfigValues = []ConfigLevel{ConfigUser, ConfigSystem}

func (c *ConfigLevel) UnmarshalText(b []byte) error {
	switch strings.ToLower(strings.TrimSpace(string(b))) {
	case "system":
		*c = ConfigSystem
	case "user":
		*c = ConfigUser
	default:
		names := make([]string, len(validLevelArgConfigValues))
		for i, lvl := range validLevelArgConfigValues {
			names[i] = lvl.String()
		}
		return fmt.Errorf("unknown config level %q, valid values are: %s", string(b), strings.Join(names, ", "))
	}
	return nil
}

func EnsureLocation(cfg Config) (string, error) {
	loc := cfg.PrimaryLocation()
	if cfg.Level == ConfigEnv && loc == "" {
		return "", errors.New("ADBC_DRIVER_PATH is empty, must be set to valid path to use")
	}

	if _, err := os.Stat(loc); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if err := hostpath.MkdirAll(loc, 0o755); err != nil {
				return "", fmt.Errorf("failed to create config directory %s: %w", loc, err)
			}
			// Create a .gitignore with "*" in it.
			//
			// This depends on the if block it's in: We only want to create this file
			// if we also had to create `loc` in the same call.
			if cfg.Level == ConfigEnv {
				gitignorePath := hostpath.Join(loc, ".gitignore")
				_ = os.WriteFile(gitignorePath, []byte("*\n"), 0o644)
			}
		} else {
			return "", fmt.Errorf("failed to stat config directory %s: %w", loc, err)
		}
	}

	return loc, nil
}

func loadConfig(lvl ConfigLevel) Config {
	cfg := Config{Level: lvl, Location: lvl.ConfigLocation()}
	if cfg.Location == "" {
		return cfg
	}

	if lvl == ConfigEnv {
		pathList := splitConfigList(cfg.Location)
		slices.Reverse(pathList)
		finalDrivers := make(map[string]DriverInfo)
		for _, p := range pathList {
			drivers, err := loadDir(p)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				cfg.Err = fmt.Errorf("error loading drivers from %s: %w", p, err)
				return cfg
			}
			maps.Copy(finalDrivers, drivers)
		}
		cfg.Exists, cfg.Drivers = len(finalDrivers) > 0, finalDrivers
		return cfg
	}

	drivers, err := loadDir(cfg.Location)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			cfg.Err = fmt.Errorf("error loading drivers from %s: %w", cfg.Location, err)
		}
		return cfg
	}

	cfg.Exists, cfg.Drivers = true, drivers
	return cfg
}

// FindDriverConfigsIn lists installed drivers from an explicit location without
// consulting environment variables, so it is safe for request-scoped concurrent
// callers. A location containing the OS list separator is treated as a path list
// (matching the env config level).
func FindDriverConfigsIn(location string) []DriverInfo {
	if location == "" {
		return nil
	}
	paths := splitConfigList(location)
	slices.Reverse(paths)
	merged := make(map[string]DriverInfo)
	for _, p := range paths {
		if p == "" {
			continue
		}
		drivers, err := loadDir(p)
		if err != nil {
			continue
		}
		maps.Copy(merged, drivers)
	}
	return slices.Collect(maps.Values(merged))
}

func getEnvConfigDir() string {
	envConfigLoc := splitConfigList(os.Getenv(adbcEnvVar))
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		envConfigLoc = append(envConfigLoc, hostpath.Join(venv, "etc", "adbc", "drivers"))
	}

	if conda := os.Getenv("CONDA_PREFIX"); conda != "" {
		envConfigLoc = append(envConfigLoc, hostpath.Join(conda, "etc", "adbc", "drivers"))
	}

	envConfigLoc = slices.DeleteFunc(envConfigLoc, func(s string) bool {
		return s == ""
	})

	return strings.Join(envConfigLoc, string(hostpath.ListSeparator()))
}

// InstallDriver extracts a package into the legacy archive-derived directory and returns its Manifest.
// It does not update runtime registration, create an install receipt, or participate in
// InstallPackage's transaction lock. Pairing it with CreateManifest is not one transaction.
//
// Deprecated: use InstallPackage for transactional package installation.
func InstallDriver(cfg Config, shortName string, downloaded *os.File) (Manifest, error) {
	var (
		loc string
		err error
	)
	if loc, err = EnsureLocation(cfg); err != nil {
		return Manifest{}, fmt.Errorf("could not ensure config location: %w", err)
	}
	base := strings.TrimSuffix(strings.TrimSuffix(hostpath.Base(downloaded.Name()), ".tar.gz"), ".tgz")
	finalDir := hostpath.Join(loc, base)

	if err := hostpath.MkdirAll(finalDir, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("failed to create driver directory %s: %w", finalDir, err)
	}

	manifest, err := InflateTarball(downloaded, finalDir)
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to extract tarball: %w", err)
	}

	driverPath := hostpath.Join(finalDir, manifest.Files.Driver)

	manifest.DriverInfo.ID = shortName
	manifest.DriverInfo.Source = "dbc"
	manifest.DriverInfo.Driver.Shared = driverMap{}
	manifest.DriverInfo.Driver.Shared.Set(PlatformTuple(), driverPath)

	return manifest, nil
}

func decodeManifest(r io.Reader, driverName string, requireShared bool) (Manifest, error) {
	var di tomlDriverInfo
	if err := toml.NewDecoder(r).Decode(&di); err != nil {
		return Manifest{}, fmt.Errorf("error decoding manifest: %w", err)
	}

	if di.ManifestVersion > currentManifestVersion {
		return Manifest{}, fmt.Errorf("manifest version %d is unsupported, only %d and lower are supported by this version of dbc",
			di.ManifestVersion, currentManifestVersion)
	}

	// Callers can assume these fields are set so return an error if they aren't
	if di.Name == "" {
		return Manifest{}, fmt.Errorf("%w: name is required", ErrInvalidManifest)
	}
	if di.Version == nil {
		return Manifest{}, fmt.Errorf("%w: version is required", ErrInvalidManifest)
	}

	result := Manifest{
		DriverInfo: DriverInfo{
			ID:        driverName,
			Name:      di.Name,
			Publisher: di.Publisher,
			License:   di.License,
			Version:   di.Version,
			Source:    di.Source,
			AdbcInfo:  di.AdbcInfo,
		},
		Files:       di.Files,
		PostInstall: di.PostInstall,
	}

	result.Driver.Entrypoint = di.Driver.Entrypoint
	switch s := di.Driver.Shared.(type) {
	case string:
		result.Driver.Shared.defaultPath = s
	case map[string]any:
		result.Driver.Shared.platformMap = make(map[string]string)
		for k, v := range s {
			if strVal, ok := v.(string); ok {
				result.Driver.Shared.platformMap[k] = strVal
			} else {
				return Manifest{}, fmt.Errorf("%w: invalid type for platform %s, expected string", ErrInvalidManifest, k)
			}
		}
	default:
		if requireShared {
			return Manifest{}, fmt.Errorf("%w: invalid type for 'Driver.shared' in manifest, expected string or table", ErrInvalidManifest)
		}
	}

	return result, nil
}

// UninstallDriverShared only supports non-dbc registrations. Since version
// 0.4.0, dbc registrations must use UninstallDriver with its Config so package
// ownership, registration scope, and sibling references can be verified.
func UninstallDriverShared(info DriverInfo) error {
	return uninstallDriverSharedForConfig(Config{Level: ConfigUnknown}, info)
}

func uninstallDriverSharedForConfig(cfg Config, info DriverInfo) error {
	if cfg.Level == ConfigUnknown {
		cfg.Location = ConfigEnv.ConfigLocation()
	}
	return uninstallDriverSharedWithOperations(cfg, info, packageCleanupOperations{})
}

func uninstallDriverSharedWithOperations(cfg Config, info DriverInfo, operations packageCleanupOperations) error {
	cfg = packageCleanupConfig(cfg, info)
	if info.Source == "dbc" {
		return errors.New("UninstallDriverShared does not remove dbc package payloads; use UninstallDriver(cfg, info)")
	}
	registrations, certain, _ := collectRegistrationSharedMapsExcluding(cfg, info.FilePath, info.FilePath, info.ID)
	return uninstallDriverSharedWithReferences(cfg, info, operations, registrations, certain)
}

func uninstallDriverSharedWithReferences(cfg Config, info DriverInfo, operations packageCleanupOperations, registrations []driverMap, referencesCertain bool) error {
	if !referencesCertain || !supportsPinnedCleanup() {
		// Keep unmanaged files when references cannot be ruled out or this host
		// cannot provide the pinned-root confinement used by safe cleanup.
		return nil
	}

	// For the User and System config levels, info.FilePath is set to the
	// appropriate registry key instead of the filesystem on windows so we
	// handle that here first.
	filesystemLocation := info.FilePath
	if strings.Contains(info.FilePath, "HKCU\\") {
		filesystemLocation = ConfigUser.ConfigLocation()
	} else if strings.Contains(info.FilePath, "HKLM\\") {
		filesystemLocation = ConfigSystem.ConfigLocation()
	}

	root, err := os.OpenRoot(filesystemLocation)
	if err != nil {
		return fmt.Errorf("error opening driver path %s: %w", info.FilePath, err)
	}
	defer root.Close()

	for registeredPath := range info.Driver.Shared.Paths() {
		if hasParentTraversal(registeredPath) {
			continue
		}
		sharedPath, err := resolveRegistrationSharedPath(filesystemLocation, registeredPath)
		if err != nil {
			continue
		}
		if sharedPathReferencedByRegistrations(sharedPath, registrations) {
			continue
		}

		relativePath, err := hostpath.Rel(filesystemLocation, sharedPath)
		if err != nil || hostpath.IsAbs(relativePath) || relativePath == "." || hasParentTraversal(relativePath) {
			// If the reference is outside the registration root or uncertain,
			// retain it rather than allowing cleanup to escape the root.
			continue
		}
		lexicalTarget := hostpath.Join(filesystemLocation, relativePath)
		if !safeToRemoveUnmanagedSharedFile(lexicalTarget) {
			continue
		}
		if operations.beforeRemove != nil {
			if err := operations.beforeRemove(lexicalTarget); err != nil {
				return fmt.Errorf("error removing driver %s: %w", info.ID, err)
			}
		}
		if err := root.Remove(relativePath); err != nil {
			// Ignore only when not found. This supports manifest-only drivers.
			// TODO: Come up with a better mechanism to handle manifest-only drivers
			// and remove this continue when we do
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("error removing driver %s: %w", info.ID, err)
		}
	}

	return nil
}

func sharedPathReferencedByRegistrations(sharedPath string, registrations []driverMap) bool {
	target, targetCertain := canonicalFilesystemPath(sharedPath)
	if !targetCertain {
		return true
	}
	for _, registration := range registrations {
		for path := range registration.Paths() {
			if path == "" {
				continue
			}
			if sameFilesystemPathOrUncertain(target, path) {
				return true
			}
		}
	}
	return false
}
