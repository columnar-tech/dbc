// Copyright (c) 2025 Columnar Technologies.  All rights reserved.

//go:build !windows

package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSystemConfigDir(t *testing.T) {
	prefix := os.Getenv("VIRTUAL_ENV")
	if prefix == "" {
		prefix = os.Getenv("CONDA_PREFIX")
	}
	assert.Equal(t, ConfigSystem.configLocation(), prefix+"/etc/adbc")
}
