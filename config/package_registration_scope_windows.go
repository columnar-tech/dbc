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

import (
	"fmt"
	"strings"
)

func packageRegistrationScopeForConfig(cfg Config) (packageRegistrationScope, error) {
	switch cfg.Level {
	case ConfigEnv:
		return packageRegistrationFile, nil
	case ConfigUser:
		return packageRegistrationRegistryUser, nil
	case ConfigSystem:
		return packageRegistrationRegistrySystem, nil
	default:
		return "", fmt.Errorf("unsupported config level %d", cfg.Level)
	}
}

func packageCleanupConfig(cfg Config, info DriverInfo) Config {
	if cfg.Level != ConfigUnknown {
		return cfg
	}
	switch {
	case strings.Contains(info.FilePath, "HKCU\\"):
		cfg.Level = ConfigUser
	case strings.Contains(info.FilePath, "HKLM\\"):
		cfg.Level = ConfigSystem
	default:
		cfg.Level = ConfigEnv
	}
	return cfg
}
