// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

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
	defaultSysConfigDir    = "/etc/adbc"
	userConfigSuffixDarwin = "ADBC"
	userConfigSuffixOther  = "adbc"
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

	// check for venv first, then a conda environment if we're not in
	// a python venv. In both cases, if we're in a virtual environment,
	// then use the virtual environment as a prefix for the system dir
	if venv, ok := os.LookupEnv("VIRTUAL_ENV"); ok && venv != "" {
		systemConfigDir = filepath.Join(venv, defaultSysConfigDir)
	} else if venv, ok := os.LookupEnv("CONDA_PREFIX"); ok && venv != "" {
		systemConfigDir = filepath.Join(venv, defaultSysConfigDir)
	}
}

func (c ConfigLevel) configLocation() string {
	switch c {
	case ConfigSystem:
		return systemConfigDir
	case ConfigUser:
		return userConfigDir
	case ConfigEnv:
		return os.Getenv(adbcEnvVar)
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
	return createDriverManifest(cfg.Location, driver)
}

func DeleteDriver(cfg Config, info DriverInfo) error {
	if info.Source == "dbc" {
		for sharedPath := range info.Driver.Shared.Paths() {
			if err := os.RemoveAll(filepath.Dir(sharedPath)); err != nil {
				return fmt.Errorf("error removing driver %s: %w", info.ID, err)
			}
		}
	} else {
		for sharedPath := range info.Driver.Shared.Paths() {
			if err := os.Remove(sharedPath); err != nil {
				return fmt.Errorf("error removing driver %s: %w", info.ID, err)
			}
		}
	}

	manifest := filepath.Join(cfg.Location, info.ID+".toml")
	if err := os.Remove(manifest); err != nil {
		return fmt.Errorf("error removing manifest %s: %w", manifest, err)
	}

	return nil
}
