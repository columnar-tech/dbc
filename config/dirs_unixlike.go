// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

//go:build !windows

package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

const (
	systemConfigDir  = "/etc/adbc"
	userConfigSuffix = "adbc"
)

var userConfigDir string

func init() {
	userConfigDir, _ = os.UserConfigDir()
	if userConfigDir != "" {
		userConfigDir = filepath.Join(userConfigDir, userConfigSuffix)
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
