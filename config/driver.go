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

	"github.com/BurntSushi/toml"
	"github.com/Masterminds/semver/v3"
)

type DriverInfo struct {
	ID string `toml:"-"`

	Name      string          `toml:"name"`
	Publisher string          `toml:"publisher"`
	License   string          `toml:"license"`
	Version   *semver.Version `toml:"version"`
	Source    string          `toml:"source"`

	AdbcInfo struct {
		Version  string `toml:"version"`
		Features struct {
			Supported   []string `toml:"supported,omitempty"`
			Unsupported []string `toml:"unsupported,omitempty"`
		} `toml:"features,omitempty"`
	} `toml:"ADBC"`

	Driver struct {
		Entrypoint string    `toml:"entrypoint,omitempty"`
		Shared     driverMap `toml:"shared"`
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

func (d *driverMap) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		d.defaultPath = v
	case map[string]any:
		d.platformMap = make(map[string]string, len(v))
		for k, val := range v {
			if strVal, ok := val.(string); ok {
				d.platformMap[k] = strVal
			} else {
				return fmt.Errorf("expected string value for platform %s, got %T", k, val)
			}
		}
	default:
		return fmt.Errorf("expected string or map[string]string, got %T", data)
	}
	return nil
}

func loadDriverFromManifest(prefix, driverName string) (DriverInfo, error) {
	manifest := filepath.Join(prefix, driverName+".toml")
	var di DriverInfo
	md, err := toml.DecodeFile(manifest, &di)
	if err != nil {
		return DriverInfo{}, fmt.Errorf("error decoding manifest %s: %w", manifest, err)
	}

	if !md.IsDefined("Driver", "shared") {
		return DriverInfo{}, fmt.Errorf("manifest %s does not define 'Driver.shared' which is a required field", manifest)
	}

	di.ID = driverName
	return di, nil
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

	if err := toml.NewEncoder(f).Encode(driver); err != nil {
		return fmt.Errorf("error encoding manifest %s: %w", driver.ID, err)
	}

	return nil
}
