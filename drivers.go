// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package dbc

import (
	_ "embed"
	"io/fs"
	"os"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/pelletier/go-toml/v2"
)

//go:embed sample_known_drivers.yaml
var driversYAML []byte

var getDrivers = sync.OnceValues(func() ([]Driver, error) {
	var drivers []Driver
	if err := yaml.Unmarshal(driversYAML, &drivers); err != nil {
		return nil, err
	}

	return drivers, nil
})

type Driver struct {
	Title    string   `yaml:"name"`
	Desc     string   `yaml:"description"`
	Type     string   `yaml:"type"`
	Versions []string `yaml:"available"`
}

func GetDriverList() ([]Driver, error) {
	return getDrivers()
}

type DriverInfo struct {
	ID string `toml:"-"`

	Name      string
	Publisher string
	License   string
	Version   string
	Source    string

	AdbcInfo struct {
		Version  string
		Features struct {
			Supported   []string
			Unsupported []string
		} `toml:"features"`
	} `toml:"ADBC"`

	Driver struct {
		Shared string
	}
}

func FindDriverConfigs(dir string) []DriverInfo {
	out := []DriverInfo{}
	if dir == "" {
		return out
	}

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
		out = append(out, di)
	}
	return out
}
