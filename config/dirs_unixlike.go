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
	systemConfigDir = "/etc/adbc_drivers"
)

var userConfigDir string

func init() {
	userConfigDir, _ = os.UserConfigDir()
	if userConfigDir != "" {
		userConfigDir = filepath.Join(userConfigDir, "adbc_drivers")
	}
}

func Get() map[ConfigLevel]Config {
	configs := make(map[ConfigLevel]Config)
	configs[ConfigSystem] = loadDir(ConfigSystem, systemConfigDir)
	if userConfigDir != "" {
		configs[ConfigUser] = loadDir(ConfigUser, userConfigDir)
	}

	if envDir := os.Getenv(adbcEnvVar); envDir != "" {
		dir, _ := filepath.Abs(envDir)
		configs[ConfigEnv] = loadDir(ConfigEnv, dir)
	}

	return configs
}

func FindDriverConfigs(lvl ConfigLevel) []DriverInfo {
	out := []DriverInfo{}

	var dir string
	switch lvl {
	case ConfigSystem:
		dir = systemConfigDir
	case ConfigUser:
		dir = userConfigDir
	case ConfigEnv:
		dir = os.Getenv(adbcEnvVar)
	}

	if dir == "" {
		return out
	}

	drv := loadDir(lvl, dir)
	return slices.Collect(maps.Values(drv.Drivers))
}

func GetDriver(cfg Config, driverName string) (DriverInfo, error) {
	return loadDriverFromManifest(cfg.Location, driverName)
}

func CreateManifest(cfg Config, driver DriverInfo) (err error) {
	return createDriverManifest(cfg.Location, driver)
}

func DeleteDriver(cfg Config, info DriverInfo) error {
	if info.Source == "dbc" {
		if err := os.RemoveAll(filepath.Dir(info.Driver.Shared)); err != nil {
			return fmt.Errorf("error removing driver %s: %w", info.ID, err)
		}
	} else {
		if err := os.Remove(info.Driver.Shared); err != nil {
			return fmt.Errorf("error removing driver %s: %w", info.ID, err)
		}
	}

	manifest := filepath.Join(cfg.Location, info.ID+".toml")
	if err := os.Remove(manifest); err != nil {
		return fmt.Errorf("error removing manifest %s: %w", manifest, err)
	}

	return nil
}
