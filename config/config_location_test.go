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
	"path/filepath"
	"runtime"
	"testing"
)

func TestConfigPrimaryLocation(t *testing.T) {
	t.Run("empty environment location", func(t *testing.T) {
		if got := (Config{Level: ConfigEnv}).PrimaryLocation(); got != "" {
			t.Fatalf("PrimaryLocation() = %q, want empty", got)
		}
	})

	t.Run("non-environment location is unchanged", func(t *testing.T) {
		location := "first" + string(filepath.ListSeparator) + "second"
		for _, level := range []ConfigLevel{ConfigUser, ConfigSystem} {
			if got := (Config{Level: level, Location: location}).PrimaryLocation(); got != location {
				t.Errorf("level %s PrimaryLocation() = %q, want %q", level, got, location)
			}
		}
	})

	if runtime.GOOS == "js" {
		for _, location := range []string{`C:\dbc:drivers;secondary`, `/tmp/dbc:drivers;secondary`} {
			if got := (Config{Level: ConfigEnv, Location: location}).PrimaryLocation(); got != location {
				t.Errorf("Wasm PrimaryLocation() = %q, want whole path %q", got, location)
			}
		}
		return
	}

	t.Run("native environment list uses first root", func(t *testing.T) {
		location := "first" + string(filepath.ListSeparator) + "second"
		if got := (Config{Level: ConfigEnv, Location: location}).PrimaryLocation(); got != "first" {
			t.Fatalf("native PrimaryLocation() = %q, want first", got)
		}
	})
}
