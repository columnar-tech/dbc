// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

//go:build !windows

package config

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSystemConfigDir(t *testing.T) {
	os := runtime.GOOS

	if os == "darwin" {
		assert.Equal(t, "/Library/Application Support/ADBC/Drivers", ConfigSystem.configLocation())
	} else {
		assert.Equal(t, "/etc/adbc/drivers", ConfigSystem.configLocation())
	}

}

func TestSystemConfigWithEnv(t *testing.T) {
	prefix := os.Getenv("VIRTUAL_ENV")
	if prefix == "" {
		prefix = os.Getenv("CONDA_PREFIX")
	}
	if prefix != "" {
		assert.Equal(t, prefix+"/etc/adbc/drivers", ConfigSystem.configLocation())
	}
}
