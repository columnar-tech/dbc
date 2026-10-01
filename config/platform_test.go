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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlatformUnmarshalText(t *testing.T) {
	valid := []string{
		"linux_amd64",
		"linux_arm64",
		"macos_amd64",
		"macos_arm64",
		"windows_amd64",
		"windows_arm64",
	}
	for _, tuple := range valid {
		var p Platform
		assert.NoError(t, p.UnmarshalText([]byte(tuple)), "tuple %q", tuple)
		assert.Equal(t, Platform(tuple), p)
	}

	invalid := []string{
		"darwin_arm64", // wrong OS name
		"linux", // missing arch
		"junk", // invalid name
	    "LINUX_AMD64", // wrong case
	}
	for _, tuple := range invalid {
		var p Platform
		err := p.UnmarshalText([]byte(tuple))
		assert.ErrorContains(t, err, "unknown platform")
		assert.ErrorContains(t, err, "valid values are:")
	}
}

func TestPlatformResolve(t *testing.T) {
	assert.Equal(t, PlatformTuple(), Platform("").Resolve())
	assert.Equal(t, "linux_amd64", Platform("linux_amd64").Resolve())
}
