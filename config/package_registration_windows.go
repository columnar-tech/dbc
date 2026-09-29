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

//go:build windows

package config

import "fmt"

func createRuntimeRegistrationUnlocked(cfg Config, location string, driver DriverInfo) error {
	if cfg.Level == ConfigEnv {
		return createDriverManifestUnlocked(location, driver)
	}
	return createRegistryManifestUnlocked(cfg, driver)
}

func packageInstallLockLocation(cfg Config, payloadLocation string) (string, error) {
	if cfg.Level == ConfigEnv {
		return payloadLocation, nil
	}
	location, err := registrationLockLocation(cfg)
	if err != nil {
		return "", fmt.Errorf("resolve registration lock location: %w", err)
	}
	return location, nil
}

func preparePackageInstallLockLocation(cfg Config, lockLocation string) error {
	if cfg.Level == ConfigEnv {
		return nil
	}
	return prepareRegistrationLockLocation(cfg, lockLocation)
}
