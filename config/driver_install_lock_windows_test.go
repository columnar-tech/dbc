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

//go:build windows

package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDriverInstallLockPathIgnoresCaseOnWindows(t *testing.T) {
	location := filepath.Join(t.TempDir(), "InstallRoot")
	original, err := driverInstallLockPath(location, "MyDriver")
	if err != nil {
		t.Fatal(err)
	}
	caseVariant, err := driverInstallLockPath(strings.ToUpper(location), "mydriver")
	if err != nil {
		t.Fatal(err)
	}
	if original != caseVariant {
		t.Fatalf("Windows lock paths should ignore location and runtime ID case: %q != %q", original, caseVariant)
	}
}

func TestWindowsUninstallLockSupportsMissingRegistryRoot(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "registry-only")
	cfg := Config{Level: ConfigUser, Location: missingRoot}
	if err := prepareDriverUninstallLockLocation(cfg, missingRoot); err != nil {
		t.Fatalf("prepare missing registration root: %v", err)
	}
	lock, err := acquireDriverInstallLockWith(t.Context(), missingRoot, "driver", 0)
	if err != nil {
		t.Fatalf("acquire uninstall lock in created root: %v", err)
	}
	if err := lock.release(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsNonEnvUninstallLockIgnoresCustomLocation(t *testing.T) {
	for _, level := range []ConfigLevel{ConfigUser, ConfigSystem} {
		t.Run(level.String(), func(t *testing.T) {
			first, err := uninstallLockLocation(Config{Level: level, Location: filepath.Join(t.TempDir(), "first")}, DriverInfo{ID: "driver"})
			if err != nil {
				t.Fatal(err)
			}
			second, err := uninstallLockLocation(Config{Level: level, Location: filepath.Join(t.TempDir(), "second")}, DriverInfo{ID: "driver"})
			if err != nil {
				t.Fatal(err)
			}
			if first != second || first != level.ConfigLocation() {
				t.Fatalf("custom locations split registry lock: %q != %q; want %q", first, second, level.ConfigLocation())
			}
			firstLock, err := driverInstallLockPath(first, "driver")
			if err != nil {
				t.Fatal(err)
			}
			secondLock, err := driverInstallLockPath(second, "driver")
			if err != nil {
				t.Fatal(err)
			}
			if firstLock != secondLock {
				t.Fatalf("registry lock paths differ: %q != %q", firstLock, secondLock)
			}
		})
	}
}

func TestWindowsUninstallLockUsesDefaultLocation(t *testing.T) {
	for _, level := range []ConfigLevel{ConfigUser, ConfigSystem} {
		t.Run(level.String(), func(t *testing.T) {
			cfg := Config{Level: level}
			location, err := uninstallLockLocation(cfg, DriverInfo{ID: "driver"})
			if err != nil {
				t.Fatal(err)
			}
			if want := level.ConfigLocation(); location != want {
				t.Fatalf("uninstall lock location = %q, want default %q", location, want)
			}
		})
	}
}

func TestWindowsUninstallLockRejectsUnknownConfigLevel(t *testing.T) {
	_, err := uninstallLockLocation(Config{Level: ConfigLevel(255)}, DriverInfo{ID: "driver"})
	if err == nil {
		t.Fatal("uninstallLockLocation accepted an unknown config level")
	}
}
