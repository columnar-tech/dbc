// Copyright 2026 Columnar Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"os"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc"
	"github.com/columnar-tech/dbc/config"
	"github.com/pelletier/go-toml/v2"
)

type DriversList struct {
	Registries      []dbc.RegistryEntry   `toml:"registries,omitempty"`
	ReplaceDefaults bool                  `toml:"replace_defaults,omitempty"`
	Drivers         map[string]driverSpec `toml:"drivers" comment:"dbc driver list"`
}

// applyProjectRegistries rebuilds the process-wide dbc client with the
// registry overrides declared in the project's dbc.toml, so subsequent calls
// through getDriverRegistry see the merged registry list. No-op when neither
// registries nor replace_defaults are set.
func applyProjectRegistries(list DriversList) error {
	if len(list.Registries) == 0 && !list.ReplaceDefaults {
		return nil
	}
	var replace *bool
	if list.ReplaceDefaults {
		replace = boolPtr(true)
	}
	c, err := newDBCClient(list.Registries, replace)
	if err != nil {
		return fmt.Errorf("error configuring project registries: %w", err)
	}
	setDBCClient(c)
	return nil
}

type driverSpec struct {
	Prerelease string              `toml:"prerelease,omitempty"`
	Version    *semver.Constraints `toml:"version"`
}

func GetDriverList(fname string) ([]dbc.PkgInfo, error) {
	var m DriversList
	f, err := os.Open(fname)
	if err != nil {
		return nil, fmt.Errorf("error opening driver list %s: %w", fname, err)
	}
	defer f.Close()
	if err = toml.NewDecoder(f).Decode(&m); err != nil {
		return nil, fmt.Errorf("error decoding driver list %s: %w", fname, err)
	}

	drivers, err := getDriverRegistry()
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
			return nil, fmt.Errorf("driver `%s` not found", name)
		}

		pkg, err := drv.GetWithConstraint(spec.Version, config.PlatformTuple())
		if err != nil {
			return nil, fmt.Errorf("error finding version for driver %s: %w", name, err)
		}

		pkgs = append(pkgs, pkg)
	}

	return pkgs, nil
}
