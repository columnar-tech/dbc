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

//go:build js || plan9

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupRetainsGenerationWhenRootCannotPinDirectoryIdentity(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	installInitialPackage(t, cfg)
	selected, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	generation := filepath.Dir(selected.Driver.Shared.Get(PlatformTuple()))
	if err := UninstallDriver(cfg, selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "driver.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registration was not removed: %v", err)
	}
	for _, name := range []string{packageInstallReceiptFilename, "driver.so"} {
		if _, err := os.Stat(filepath.Join(generation, name)); err != nil {
			t.Fatalf("un-pinned generation %s was not retained: %v", name, err)
		}
	}
}
