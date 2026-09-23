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
	"fmt"
	"slices"
	"strings"
)

// Platform is an optional install target platform tuple (e.g. "linux_amd64").
// When unset, callers should use PlatformTuple() for the host platform.
type Platform string

// Valid platform tuples as in https://dbc-cdn.columnar.tech/index.yaml
// Please keep up-to-date with the registry index!
var validPlatformTuples = []string{
	"linux_amd64",
	"linux_arm64",
	"macos_amd64",
	"macos_arm64",
	"windows_amd64",
	"windows_arm64",
}

func ValidPlatformTuples() []string {
	return slices.Clone(validPlatformTuples)
}

func IsValidPlatformTuple(tuple string) bool {
	return slices.Contains(validPlatformTuples, tuple)
}

func (p Platform) String() string {
	return string(p)
}

// Resolve returns the explicit platform tuple, or the host platform when unset.
func (p Platform) Resolve() string {
	if p == "" {
		return PlatformTuple()
	}
	return string(p)
}

func (p *Platform) UnmarshalText(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" {
		*p = ""
		return nil
	}
	if !IsValidPlatformTuple(s) {
		return fmt.Errorf("unknown platform %q, valid values are: %s", s, strings.Join(validPlatformTuples, ", "))
	}
	*p = Platform(s)
	return nil
}
