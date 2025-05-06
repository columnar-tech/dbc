// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type DriverInfo struct {
	ID string `toml:"-"`

	Name      string `toml:"name"`
	Publisher string `toml:"publisher"`
	License   string `toml:"license"`
	Version   string `toml:"version"`
	Source    string `toml:"source"`

	AdbcInfo struct {
		Version  string `toml:"version"`
		Features struct {
			Supported   []string `toml:"supported,omitempty"`
			Unsupported []string `toml:"unsupported,omitempty"`
		} `toml:"features,omitempty"`
	} `toml:"ADBC"`

	Driver struct {
		Shared string `toml:"shared"`
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

func GetDriver(dir, driverName string) (DriverInfo, error) {
	manifest := filepath.Join(dir, driverName+".toml")
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
