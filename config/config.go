// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package config

type Config struct {
	Level    ConfigLevel
	Location string
	Drivers  map[string]DriverInfo
	Exists   bool
	Err      error
}

type ConfigLevel int
