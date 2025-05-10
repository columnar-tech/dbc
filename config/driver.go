// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package config

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
