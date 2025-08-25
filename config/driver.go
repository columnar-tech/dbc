// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/pelletier/go-toml/v2"
)

type Manifest struct {
	DriverInfo

	Files struct {
		Driver    string `toml:"driver"`
		Signature string `toml:"signature"`
	} `toml:"Files"`

	PostInstall struct {
		Messages []string `toml:"messages,inline"`
	} `toml:"PostInstall"`
}

type DriverInfo struct {
	ID       string
	FilePath string

	Name      string
	Publisher string
	License   string
	Version   *semver.Version
	Source    string

	AdbcInfo struct {
		Version  *semver.Version `toml:"version"`
		Features struct {
			Supported   []string `toml:"supported,omitempty"`
			Unsupported []string `toml:"unsupported,omitempty"`
		} `toml:"features,omitempty"`
	} `toml:"ADBC"`

	Driver struct {
		Entrypoint string
		Shared     driverMap
	}
}

type driverMap struct {
	platformMap map[string]string
	defaultPath string
}

func (d *driverMap) Set(platformTuple, path string) {
	if d.platformMap == nil {
		d.platformMap = make(map[string]string)
	}
	d.platformMap[platformTuple] = path
}

func (d driverMap) Get(platformTuple string) string {
	if d.defaultPath != "" {
		return d.defaultPath
	}
	return d.platformMap[platformTuple]
}

func (d driverMap) Paths() iter.Seq[string] {
	if d.defaultPath != "" {
		return func(yield func(string) bool) {
			yield(d.defaultPath)
		}
	}

	return func(yield func(string) bool) {
		for _, path := range d.platformMap {
			if !yield(path) {
				return
			}
		}
	}
}

func (d driverMap) String() string {
	if d.defaultPath != "" {
		return "\t" + d.defaultPath
	}
	if len(d.platformMap) == 0 {
		return ""
	}
	var sb strings.Builder
	for platform, path := range d.platformMap {
		sb.WriteString(fmt.Sprintf("\t- %s: %s\n", platform, path))
	}
	return sb.String()
}

type tomlDriverInfo struct {
	Name      string          `toml:"name"`
	Publisher string          `toml:"publisher"`
	License   string          `toml:"license"`
	Version   *semver.Version `toml:"version"`
	Source    string          `toml:"source"`

	AdbcInfo struct {
		Version  *semver.Version `toml:"version"`
		Features struct {
			Supported   []string `toml:"supported,omitempty"`
			Unsupported []string `toml:"unsupported,omitempty"`
		} `toml:"features,omitempty"`
	} `toml:"ADBC"`

	Driver struct {
		Entrypoint string `toml:"entrypoint,omitempty"`
		Shared     any    `toml:"shared"`
	}
}

func loadDriverFromManifest(prefix, driverName string) (DriverInfo, error) {
	driverName = strings.TrimSuffix(driverName, ".toml")
	manifest := filepath.Join(prefix, driverName+".toml")
	var di tomlDriverInfo
	f, err := os.Open(manifest)
	if err != nil {
		return DriverInfo{}, fmt.Errorf("error opening manifest %s: %w", manifest, err)
	}
	defer f.Close()

	if err := toml.NewDecoder(f).Decode(&di); err != nil {
		return DriverInfo{}, fmt.Errorf("error decoding manifest %s: %w", manifest, err)
	}

	result := DriverInfo{
		ID:        driverName,
		Name:      di.Name,
		Publisher: di.Publisher,
		License:   di.License,
		Version:   di.Version,
		Source:    di.Source,
		AdbcInfo:  di.AdbcInfo,
	}

	result.Driver.Entrypoint = di.Driver.Entrypoint
	switch s := di.Driver.Shared.(type) {
	case string:
		result.Driver.Shared.defaultPath = s
	case map[string]any:
		result.Driver.Shared.platformMap = make(map[string]string)
		for k, v := range s {
			if strVal, ok := v.(string); ok {
				result.Driver.Shared.platformMap[k] = strVal
			} else {
				return DriverInfo{}, fmt.Errorf("invalid type for platform %s in manifest %s, expected string", k, manifest)
			}
		}
	default:
		return DriverInfo{}, fmt.Errorf("invalid type for 'Driver.shared' in manifest %s, expected string or table", manifest)
	}

	return result, nil
}

func createDriverManifest(location string, driver DriverInfo) error {
	if _, err := os.Stat(location); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(location, 0755); err != nil {
			return fmt.Errorf("error creating driver location %s: %w", location, err)
		}
	}

	f, err := os.Create(filepath.Join(location, driver.ID+".toml"))
	if err != nil {
		return fmt.Errorf("error creating manifest %s: %w", driver.ID, err)
	}
	defer f.Close()

	toEncode := tomlDriverInfo{
		Name:      driver.Name,
		Publisher: driver.Publisher,
		License:   driver.License,
		Version:   driver.Version,
		Source:    driver.Source,
		AdbcInfo:  driver.AdbcInfo,
	}

	toEncode.Driver.Entrypoint = driver.Driver.Entrypoint
	if driver.Driver.Shared.defaultPath != "" {
		toEncode.Driver.Shared = driver.Driver.Shared.defaultPath
	} else if len(driver.Driver.Shared.platformMap) > 0 {
		toEncode.Driver.Shared = driver.Driver.Shared.platformMap
	}

	enc := toml.NewEncoder(f).SetIndentTables(false)

	if err := enc.Encode(toEncode); err != nil {
		return fmt.Errorf("error encoding manifest %s: %w", driver.ID, err)
	}

	return nil
}
