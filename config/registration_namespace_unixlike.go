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

//go:build !windows

package config

import (
	"fmt"
	"os"

	"github.com/columnar-tech/dbc/internal/hostpath"
)

func registrationNamespaceLockSpec(_ Config, location string) (identity, lockDirectory, registrationLocation string, err error) {
	if location == "" {
		return "", "", "", fmt.Errorf("registration namespace location is empty")
	}
	absolute, err := hostpath.Abs(location)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve registration namespace location: %w", err)
	}
	resolved, err := hostpath.EvalSymlinks(hostpath.Clean(absolute))
	if err != nil {
		return "", "", "", fmt.Errorf("resolve registration namespace symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", "", fmt.Errorf("inspect registration namespace: %w", err)
	}
	if !info.IsDir() {
		return "", "", "", fmt.Errorf("registration namespace %s is not a directory", resolved)
	}
	resolved = hostpath.Clean(resolved)
	return fileRegistrationNamespaceIdentity(resolved, hostpath.IsWindows()), resolved, resolved, nil
}

func collectRegistrationSharedMaps(_ Config, location, excludedID string) ([]driverMap, bool, error) {
	return collectFileRegistrationSharedMaps(location, excludedID)
}

func readPrimaryRuntimeRegistration(_ Config, registrationLocation, runtimeID string) (DriverInfo, bool) {
	return readFileRuntimeRegistration(registrationLocation, runtimeID)
}
