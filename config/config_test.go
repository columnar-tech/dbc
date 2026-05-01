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

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigLevelUnmarshalText(t *testing.T) {
	valid := []struct {
		input string
		want  ConfigLevel
	}{
		{"user", ConfigUser},
		{"system", ConfigSystem},
		{"USER", ConfigUser},
		{"SYSTEM", ConfigSystem},
	}
	for _, tt := range valid {
		var c ConfigLevel
		assert.NoError(t, c.UnmarshalText([]byte(tt.input)))
		assert.Equal(t, tt.want, c)
	}

	invalid := []string{"env", "bad", ""}
	for _, s := range invalid {
		var c ConfigLevel
		err := c.UnmarshalText([]byte(s))
		assert.ErrorContains(t, err, "unknown config level")
		assert.ErrorContains(t, err, "valid values are: user, system")
	}
}

func TestConfigEnvVarHierarchy(t *testing.T) {
	// Test the order is honored (ADBC_DRIVER_PATH before VIRTUAL_ENV before
	// CONDA_PREFIX) and unset each one (in order) to verify.
	t.Setenv("ADBC_DRIVER_PATH", "some_adbc_driver_path")
	t.Setenv("VIRTUAL_ENV", "some_virtual_env")
	t.Setenv("CONDA_PREFIX", "some_conda_prefix")

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
