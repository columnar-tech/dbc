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
	"errors"
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

func wrapWithRegistryContext(err, registryErr error) error {
	if registryErr != nil {
		return fmt.Errorf("%w\n\nNote: Some driver registries were unavailable:\n%s", err, registryErr.Error())
	}
	return err
}

func defaultBaseModel() baseModel {
	return baseModel{
		getDriverRegistry: getDriverRegistry,
		downloadPkg:       downloadPkg,
	}
}

func openAndDecodeDriverList(path string) (DriversList, error) {
	p, err := driverListPath(path)
	if err != nil {
		return DriversList{}, err
	}

	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DriversList{}, fmt.Errorf("error opening driver list: %s doesn't exist\nDid you run `dbc init`?", path)
		}
		return DriversList{}, fmt.Errorf("error opening driver list at %s: %w", path, err)
	}
	defer f.Close()

	var list DriversList
	if err := toml.NewDecoder(f).Decode(&list); err != nil {
		return DriversList{}, err
	}
	return list, nil
}
