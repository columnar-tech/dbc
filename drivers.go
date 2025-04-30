// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package dbc

import (
	_ "embed"
	"sync"

	"github.com/goccy/go-yaml"
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
