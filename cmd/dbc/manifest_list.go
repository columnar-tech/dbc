// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package main

import (
	"fmt"

	"github.com/BurntSushi/toml"
	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
)

type ManifestList struct {
	Drivers map[string]driverSpec `toml:"drivers"`
}

type driverSpec struct {
	Version *semver.Constraints
}

func (d *driverSpec) UnmarshalTOML(data any) (err error) {
	switch v := data.(type) {
	case string:
		d.Version, err = semver.NewConstraint(v)
	case map[string]any:
		if ver, ok := v["version"]; ok {
			d.Version, err = semver.NewConstraint(fmt.Sprintf("%v", ver))
		} else {
			return fmt.Errorf("missing version in driver spec")
		}
	default:
		return fmt.Errorf("invalid driver spec format: %T", data)
	}

	return
}

func GetDriverList(fname, platformTuple string) ([]dbc.PkgInfo, error) {
	var m ManifestList
	md, err := toml.DecodeFile(fname, &m)
	if err != nil {
		return nil, fmt.Errorf("error decoding manifest file %s: %w", fname, err)
	}

	if !md.IsDefined("drivers") {
		return nil, fmt.Errorf("no drivers defined in manifest file %s", fname)
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
