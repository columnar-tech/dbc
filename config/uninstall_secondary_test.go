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

//go:build !windows && !js

package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/columnar-tech/dbc/config"
)

func TestUninstallDriverUsesSelectedSecondaryRoot(t *testing.T) {
	primary := t.TempDir()
	secondary := t.TempDir()
	cfg := config.Config{
		Level:    config.ConfigEnv,
		Location: primary + string(filepath.ListSeparator) + secondary,
	}
	secondaryInfo := makeTestDriverInfo("driver", secondary)
	secondaryInfo.Source = "external"
	secondaryLibrary := filepath.Join(secondary, "driver.so")
	if err := os.WriteFile(secondaryLibrary, []byte("secondary"), 0o600); err != nil {
		t.Fatal(err)
	}
	secondaryInfo.Driver.Shared.Set("linux_amd64", secondaryLibrary)
	if err := config.CreateManifest(config.Config{Level: config.ConfigEnv, Location: secondary}, secondaryInfo); err != nil {
		t.Fatal(err)
	}
	selected, err := config.GetDriver(cfg, secondaryInfo.ID)
	if err != nil {
		t.Fatal(err)
	}

	primaryInfo := makeTestDriverInfo("driver", primary)
	primaryInfo.Source = "external"
	primaryLibrary := filepath.Join(primary, "driver.so")
	if err := os.WriteFile(primaryLibrary, []byte("primary"), 0o600); err != nil {
		t.Fatal(err)
	}
	primaryInfo.Driver.Shared.Set("linux_amd64", primaryLibrary)
	if err := config.CreateManifest(config.Config{Level: config.ConfigEnv, Location: primary}, primaryInfo); err != nil {
		t.Fatal(err)
	}

	if err := config.UninstallDriver(cfg, selected); err != nil {
		t.Fatalf("UninstallDriver secondary registration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(secondary, "driver.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secondary registration remains, stat error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(primary, "driver.toml")); err != nil {
		t.Fatalf("primary registration was removed: %v", err)
	}
}
