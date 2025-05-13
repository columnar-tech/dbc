// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package config

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const adbcEnvVar = "ADBC_DRIVERS_DIR"

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
