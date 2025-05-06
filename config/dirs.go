// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

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

type Config struct {
	Level    ConfigLevel
	Location string
	Drivers  map[string]DriverInfo
	Exists   bool
	Err      error
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
	for _, di := range FindDriverConfigs(dir) {
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
