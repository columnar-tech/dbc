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
