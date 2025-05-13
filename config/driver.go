// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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

func loadDriverFromManifest(prefix, driverName string) (DriverInfo, error) {
	manifest := filepath.Join(prefix, driverName+".toml")
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
