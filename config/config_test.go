// Copyright 2025 Columnar Technologies Inc.
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

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigEnvVarHierarchy(t *testing.T) {
	// Record old values and reset when done
	originalAdbcConfigPath := os.Getenv("ADBC_DRIVER_PATH")
	originalVirtualEnv := os.Getenv("VIRTUAL_ENV")
	originalCondaPrefix := os.Getenv("CONDA_PREFIX")
	defer func() {
		os.Setenv("ADBC_DRIVER_PATH", originalAdbcConfigPath)
		os.Setenv("VIRTUAL_ENV", originalVirtualEnv)
		os.Setenv("CONDA_PREFIX", originalCondaPrefix)
	}()

	// Test the order is honored (ADBC_DRIVER_PATH before VIRTUAL_ENV before
	// CONDA_PREFIX) and unset each one (in order) to verify.
	os.Setenv("ADBC_DRIVER_PATH", "some_adbc_driver_path")
	os.Setenv("VIRTUAL_ENV", "some_virtual_env")
	os.Setenv("CONDA_PREFIX", "some_conda_prefix")

	cfg := loadConfig(ConfigEnv)
	assert.Equal(t, "some_adbc_driver_path"+string(filepath.ListSeparator)+
		filepath.Join("some_virtual_env", "etc", "adbc", "drivers")+
		string(filepath.ListSeparator)+
		filepath.Join("some_conda_prefix", "etc", "adbc", "drivers"), cfg.Location)

	os.Setenv("ADBC_DRIVER_PATH", "")
	cfg = loadConfig(ConfigEnv)
	assert.Equal(t, filepath.Join("some_virtual_env", "etc", "adbc", "drivers")+string(filepath.ListSeparator)+
		filepath.Join("some_conda_prefix", "etc", "adbc", "drivers"), cfg.Location)

	os.Setenv("VIRTUAL_ENV", "")
	cfg = loadConfig(ConfigEnv)
	assert.Equal(t, filepath.Join("some_conda_prefix", "etc", "adbc", "drivers"), cfg.Location)
	os.Setenv("CONDA_PREFIX", "")
	cfg = loadConfig(ConfigEnv)
	assert.Equal(t, "", cfg.Location)
}
