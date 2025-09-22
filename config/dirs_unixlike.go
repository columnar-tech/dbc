// Copyright (c) 2025 Columnar Technologies Inc.  All rights reserved.

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

func (c ConfigLevel) configLocation() string {
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
	return loadDriverFromManifest(cfg.Location, driverName)
}

func CreateManifest(cfg Config, driver DriverInfo) (err error) {
	loc, err := EnsureLocation(cfg)
	if err != nil {
		return err
	}
	return createDriverManifest(loc, driver)
}

func UninstallDriver(cfg Config, info DriverInfo) error {
	manifest := filepath.Join(cfg.Location, info.ID+".toml")
	if err := os.Remove(manifest); err != nil {
		return fmt.Errorf("error removing manifest %s: %w", manifest, err)
	}

	if err := UninstallDriverShared(cfg, info); err != nil {
		return fmt.Errorf("failed to delete driver shared object: %w", err)
	}

	return nil
}
