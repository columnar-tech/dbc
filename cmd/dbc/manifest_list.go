// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"fmt"
	"os"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/pelletier/go-toml/v2"
)

type ManifestList struct {
	Drivers map[string]driverSpec `toml:"drivers" comment:"dbc driver list"`
}

type driverSpec struct {
	Version *semver.Constraints `toml:"version"`
}

func GetDriverList(fname, platformTuple string) ([]dbc.PkgInfo, error) {
	var m ManifestList
	f, err := os.Open(fname)
	if err != nil {
		return nil, fmt.Errorf("error opening manifest file %s: %w", fname, err)
	}
	defer f.Close()
	if err = toml.NewDecoder(f).Decode(&m); err != nil {
		return nil, fmt.Errorf("error decoding manifest file %s: %w", fname, err)
	}

	drivers, err := dbc.GetDriverList()
	if err != nil {
		return nil, err
	}

	// create mapping to avoid multiple loops through
	dmap := make(map[string]dbc.Driver)
	for _, driver := range drivers {
		dmap[driver.Path] = driver
	}

	var pkgs []dbc.PkgInfo
	for name, spec := range m.Drivers {
		drv, ok := dmap[name]
		if !ok {
			return nil, fmt.Errorf("driver %s not found", name)
		}

		pkg, err := drv.GetWithConstraint(spec.Version, platformTuple)
		if err != nil {
			return nil, fmt.Errorf("error finding version for driver %s: %w", name, err)
		}

		pkgs = append(pkgs, pkg)
	}

	return pkgs, nil
}
