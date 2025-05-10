// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

//go:build !windows

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

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
		return "env (" + adbcEnvVar + ")"
	default:
		return "unknown"
	}
}

func (c *ConfigLevel) UnmarshalText(b []byte) error {
	switch strings.ToLower(strings.TrimSpace(string(b))) {
	case "system":
		*c = ConfigSystem
	case "user":
		*c = ConfigUser
	case "env":
		*c = ConfigEnv
	default:
		return errors.New("unknown config level")
	}
	return nil
}

const (
	systemConfigDir = "/etc/adbc_drivers"
	adbcEnvVar      = "ADBC_DRIVERS_DIR"
)

var userConfigDir string

func init() {
	userConfigDir, _ = os.UserConfigDir()
	if userConfigDir != "" {
		userConfigDir = filepath.Join(userConfigDir, "adbc_drivers")
	}
}

func loadDir(lvl ConfigLevel, dir string) Config {
	ret := Config{Location: dir, Level: lvl}

	if _, err := os.Stat(dir); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			ret.Err = err
		}
		return ret
	}

	ret.Exists, ret.Drivers = true, make(map[string]DriverInfo)

	fsys := os.DirFS(dir)
	matches, _ := fs.Glob(fsys, "*.toml")
	for _, m := range matches {
		var di DriverInfo
		f, err := fsys.Open(m)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		if err := toml.NewDecoder(f).Decode(&di); err != nil {
			panic(err)
		}

		di.ID = strings.TrimSuffix(m, ".toml")
		ret.Drivers[di.ID] = di
	}
	return ret
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
	manifest := filepath.Join(cfg.Location, driverName+".toml")
	f, err := os.Open(manifest)
	if err != nil {
		return DriverInfo{}, fmt.Errorf("error opening manifest %s: %w", manifest, err)
	}
	defer f.Close()

	var di DriverInfo
	if err := toml.NewDecoder(f).Decode(&di); err != nil {
		return DriverInfo{}, fmt.Errorf("error decoding manifest %s: %w", manifest, err)
	}
	di.ID = driverName
	return di, nil
}

func CreateManifest(cfg Config, driver DriverInfo) (err error) {
	f, err := os.Create(filepath.Join(cfg.Location, driver.ID+".toml"))
	if err != nil {
		return fmt.Errorf("error creating manifest %s: %w", driver.ID, err)
	}
	defer f.Close()

	if err := toml.NewEncoder(f).Encode(driver); err != nil {
		return fmt.Errorf("error encoding manifest %s: %w", driver.ID, err)
	}

	return nil
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
