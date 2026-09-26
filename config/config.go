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
	"path/filepath"
	"runtime"
	"slices"
	"strings"

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
	loc := cfg.Location
	if cfg.Level == ConfigEnv {
		list := splitConfigList(loc)
		if len(list) == 0 {
			return "", errors.New("ADBC_DRIVER_PATH is empty, must be set to valid path to use")
		}
		loc = list[0]
	}

	if _, err := os.Stat(loc); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.MkdirAll(loc, 0o755); err != nil {
				return "", fmt.Errorf("failed to create config directory %s: %w", loc, err)
			}
			// Create a .gitignore with "*" in it.
			//
			// This depends on the if block it's in: We only want to create this file
			// if we also had to create `loc` in the same call.
			if cfg.Level == ConfigEnv {
				gitignorePath := filepath.Join(loc, ".gitignore")
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
		pathList := filepath.SplitList(cfg.Location)
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
	envConfigLoc := filepath.SplitList(os.Getenv(adbcEnvVar))
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		envConfigLoc = append(envConfigLoc, filepath.Join(venv, "etc", "adbc", "drivers"))
	}

	if conda := os.Getenv("CONDA_PREFIX"); conda != "" {
		envConfigLoc = append(envConfigLoc, filepath.Join(conda, "etc", "adbc", "drivers"))
	}

	envConfigLoc = slices.DeleteFunc(envConfigLoc, func(s string) bool {
		return s == ""
	})

	return strings.Join(envConfigLoc, string(filepath.ListSeparator))
}

func InstallDriver(cfg Config, shortName string, downloaded *os.File) (Manifest, error) {
	if downloaded == nil {
		return Manifest{}, errors.New("package archive is nil")
	}
	defer downloaded.Close()
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(downloaded.Name()), ".tar.gz"), ".tgz")
	expected := ExpectedPackageMetadata{
		ID: shortName, SourceType: "dbc", SourceIdentity: "legacy-install",
	}
	return installPackageArchive(cfg, base, shortName, downloaded, expected)
}

// TODO: Unexport once we refactor sync.go. sync.go has it's own separate
// installation routine which it probably shouldn't.
func InflateTarball(f *os.File, outDir string) (Manifest, error) {
	if f == nil {
		return Manifest{}, errors.New("package archive is nil")
	}
	defer f.Close()
	info, err := os.Stat(outDir)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not access output directory %s: %w", outDir, err)
	}
	if !info.IsDir() {
		return Manifest{}, fmt.Errorf("output path %s is not a directory", outDir)
	}
	workDir, err := os.MkdirTemp(outDir, ".dbc-inflate-")
	if err != nil {
		return Manifest{}, fmt.Errorf("could not create private extraction staging directory: %w", err)
	}
	defer os.RemoveAll(workDir)
	archivePath := filepath.Join(workDir, "archive.tgz")
	if _, _, err := snapshotArchive(f, archivePath); err != nil {
		return Manifest{}, fmt.Errorf("could not snapshot archive: %w", err)
	}
	payloadDir := filepath.Join(workDir, "payload")
	if err := os.Mkdir(payloadDir, 0o700); err != nil {
		return Manifest{}, fmt.Errorf("could not create private extraction directory: %w", err)
	}
	manifest, files, err := extractPackageArchive(archivePath, payloadDir)
	if err != nil {
		return Manifest{}, fmt.Errorf("could not extract tarball: %w", err)
	}
	names := make([]string, 0, len(files))
	for _, name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	if err := publishExtractedFiles(payloadDir, outDir, names); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func decodeManifest(r io.Reader, driverName string, requireShared bool) (Manifest, error) {
	var di runtimeManifestWire
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

	shared, err := decodeDriverShared(di.Driver.Shared, requireShared)
	if err != nil {
		return Manifest{}, err
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
	result.Driver.Shared = shared

	return result, nil
}

// Common, non-platform-specific code for uninstalling a driver. Called by
// platform-specific UninstallDriver function.
func UninstallDriverShared(info DriverInfo) error {
	// For the User and System config levels, info.FilePath is set to the
	// appropriate registry key instead of the filesystem on windows so we
	// handle that here first.
	filesystemLocation := info.FilePath
	if strings.Contains(info.FilePath, "HKCU\\") {
		filesystemLocation = ConfigUser.ConfigLocation()
	} else if strings.Contains(info.FilePath, "HKLM\\") {
		filesystemLocation = ConfigSystem.ConfigLocation()
	}
	if info.Source == "dbc" {
		return cleanupOwnedPackageDirectories(filesystemLocation, info.ID, &info, "", DriverInfo{})
	}

	root, err := os.OpenRoot(filesystemLocation)
	if err != nil {
		return fmt.Errorf("error opening driver path %s: %w", info.FilePath, err)
	}
	defer root.Close()

	for sharedPath := range info.Driver.Shared.Paths() {
		// Make sharedPath relative to info.FilePath and use it within root
		// to ensure that nothing can escape the intended directory.
		// (i.e. avoid malicious driver manifests)
		sharedPath, err = filepath.Rel(filesystemLocation, sharedPath)
		if err != nil {
			// If we can't make it relative, something is wrong, skip
			continue
		}

		if err := root.Remove(sharedPath); err != nil {
			// Ignore only when not found. This supports manifest-only drivers.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("error removing driver %s: %w", info.ID, err)
		}
	}

	// Preserve the historical non-dbc cleanup behavior for guessed directories.
	// dbc packages use receipt or legacy ownership checks above.
	extraFolder := fmt.Sprintf("%s_%s_v%s", info.ID, platformTuple, info.Version)
	extraFolder = filepath.Clean(extraFolder)
	finfo, err := root.Stat(extraFolder)
	if err == nil && finfo.IsDir() && extraFolder != "." {
		_ = root.RemoveAll(extraFolder)
		// ignore errors
	}

	return nil
}

func cleanupUninstalledDriverPackagesWithRemoveAll(cfg Config, info DriverInfo, removeAll func(string) error) error {
	if info.Source != "dbc" {
		return nil
	}
	location, err := uninstallPackageCleanupLocation(cfg, info)
	if err != nil {
		return fmt.Errorf("could not resolve package cleanup location: %w", err)
	}
	return cleanupOwnedPackageDirectoriesWithRemoveAll(location, info.ID, &info, "", DriverInfo{}, removeAll)
}

func cleanupUninstalledDriverPackages(cfg Config, info DriverInfo) error {
	return cleanupUninstalledDriverPackagesWithRemoveAll(cfg, info, os.RemoveAll)
}

func cleanupUninstalledDriverPackagesAfterRegistrationRemoval(cfg Config, info DriverInfo) error {
	return packageCleanupAfterUninstallError(cleanupUninstalledDriverPackages(cfg, info))
}

func cleanupUninstalledDriverPackagesAfterRegistrationRemovalWithRemoveAll(cfg Config, info DriverInfo, removeAll func(string) error) error {
	return packageCleanupAfterUninstallError(cleanupUninstalledDriverPackagesWithRemoveAll(cfg, info, removeAll))
}

func packageCleanupAfterUninstallError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("driver registration was removed but owned package cleanup was incomplete: %w", err)
}
