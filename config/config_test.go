// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigEnvVarHierarchy(t *testing.T) {
	// Record old values and reset when done
	originalAdbcConfigPath := os.Getenv("ADBC_CONFIG_PATH")
	originalVirtualEnv := os.Getenv("VIRTUAL_ENV")
	originalCondaPrefix := os.Getenv("CONDA_PREFIX")
	defer func() {
		os.Setenv("ADBC_CONFIG_PATH", originalAdbcConfigPath)
		os.Setenv("VIRTUAL_ENV", originalVirtualEnv)
		os.Setenv("CONDA_PREFIX", originalCondaPrefix)
	}()

	// Test the order is honored (ADBC_CONFIG_PATH before VIRTUAL_ENV before
	// CONDA_PREFIX) and unset each one (in order) to verify.
	os.Setenv("ADBC_CONFIG_PATH", "some_adbc_config_path")
	os.Setenv("VIRTUAL_ENV", "some_virtual_env")
	os.Setenv("CONDA_PREFIX", "some_conda_prefix")

	cfg := loadConfig(ConfigEnv)
	assert.Equal(t, "some_adbc_config_path"+string(filepath.ListSeparator)+filepath.Join("some_virtual_env", "etc", "adbc")+string(filepath.ListSeparator)+filepath.Join("some_conda_prefix", "etc", "adbc"), cfg.Location)

	os.Setenv("ADBC_CONFIG_PATH", "")
	cfg = loadConfig(ConfigEnv)
	assert.Equal(t, filepath.Join("some_virtual_env", "etc", "adbc")+string(filepath.ListSeparator)+filepath.Join("some_conda_prefix", "etc", "adbc"), cfg.Location)

	os.Setenv("VIRTUAL_ENV", "")
	cfg = loadConfig(ConfigEnv)
	assert.Equal(t, filepath.Join("some_conda_prefix", "etc", "adbc"), cfg.Location)
	os.Setenv("CONDA_PREFIX", "")
	cfg = loadConfig(ConfigEnv)
	assert.Equal(t, "", cfg.Location)
}
