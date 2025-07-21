// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

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

	"github.com/BurntSushi/toml"
)

const adbcEnvVar = "ADBC_CONFIG_PATH"

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

func loadDir(dir string) (map[string]DriverInfo, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}

	ret := make(map[string]DriverInfo)

	fsys := os.DirFS(dir)
	matches, _ := fs.Glob(fsys, "*.toml")
	for _, m := range matches {
		var di DriverInfo
		if _, err := toml.DecodeFile(filepath.Join(dir, m), &di); err != nil {
			panic(err)
		}

		di.ID = strings.TrimSuffix(m, ".toml")
		ret[di.ID] = di
	}
	return ret, nil
}

func loadConfig(lvl ConfigLevel) Config {
	cfg := Config{Level: lvl, Location: lvl.configLocation()}
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
