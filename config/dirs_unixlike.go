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
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
)

const (
	// defaultSysConfigDir is used on non-macOS but also on macOS when in a
	// python/conda environment (i.e., $VIRTUAL_ENV/etc/adbc)
	defaultSysConfigDir = "/etc/adbc/drivers"
	sysConfigDirDarwin  = "/Library/Application Support/ADBC/Drivers"

	userConfigSuffixDarwin = "ADBC/Drivers"
	userConfigSuffixOther  = "adbc/drivers"
)

var (
	userConfigDir   string
	systemConfigDir = defaultSysConfigDir
)

func platformUserConfigSuffix() string {
	os := runtime.GOOS

	if os == "darwin" {
		return userConfigSuffixDarwin
	}

	return userConfigSuffixOther
}

func init() {
	userConfigDir, _ = os.UserConfigDir()
	if userConfigDir != "" {
		userConfigDir = filepath.Join(userConfigDir, platformUserConfigSuffix())
	}

	if runtime.GOOS == "darwin" {
		systemConfigDir = sysConfigDirDarwin
	} else {
		systemConfigDir = defaultSysConfigDir
	}
}

func (c ConfigLevel) ConfigLocation() string {
	switch c {
	case ConfigSystem:
		return systemConfigDir
	case ConfigUser:
		return userConfigDir
	case ConfigEnv:
		return getEnvConfigDir()
	default:
		panic("unknown config level")
	}
}

func Get() map[ConfigLevel]Config {
	configs := make(map[ConfigLevel]Config)
	configs[ConfigSystem] = loadConfig(ConfigSystem)
	if userConfigDir != "" {
		configs[ConfigUser] = loadConfig(ConfigUser)
	}
	configs[ConfigEnv] = loadConfig(ConfigEnv)

	return configs
}

func FindDriverConfigs(lvl ConfigLevel) []DriverInfo {
	return slices.Collect(maps.Values(loadConfig(lvl).Drivers))
}

func GetDriver(cfg Config, driverName string) (DriverInfo, error) {
	if cfg.Level == ConfigEnv {
		for _, prefix := range filepath.SplitList(cfg.Location) {
			if di, err := loadDriverFromManifest(prefix, driverName); err == nil {
				return di, nil
			}
		}
		return DriverInfo{}, fmt.Errorf("searched %s", cfg.Location)
	}

	return loadDriverFromManifest(cfg.Location, driverName)
}

func CreateManifest(cfg Config, driver DriverInfo) (err error) {
	loc, err := EnsureLocation(cfg)
	if err != nil {
		return err
	}
	return createDriverManifest(loc, driver)
}

func UninstallDriver(_ Config, info DriverInfo) error {
	manifest := filepath.Join(info.FilePath, info.ID+".toml")
	if err := os.Remove(manifest); err != nil {
		return fmt.Errorf("error removing manifest %s: %w", manifest, err)
	}

	// Remove the symlink created during installation (one level up from the
	// manifest)
	// TODO: Remove this when the driver managers are fixed (>=1.8.1).
	removeManifestSymlink(info.FilePath, info.ID)

	if err := UninstallDriverShared(info); err != nil {
		return fmt.Errorf("failed to delete driver shared object: %w", err)
	}

	return nil
}
