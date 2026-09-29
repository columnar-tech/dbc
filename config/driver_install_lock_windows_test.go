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
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/columnar-tech/dbc/internal/fslock"
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

func TestWindowsPackageInstallUsesRegistrationLockLocation(t *testing.T) {
	for _, level := range []ConfigLevel{ConfigUser, ConfigSystem} {
		t.Run(level.String(), func(t *testing.T) {
			cfg := Config{Level: level, Location: filepath.Join(t.TempDir(), "custom-payload-root")}
			lockLocation, err := packageInstallLockLocation(cfg, cfg.Location)
			if err != nil {
				t.Fatal(err)
			}
			if want := level.ConfigLocation(); lockLocation != want {
				t.Fatalf("package install lock location = %q, want registration location %q", lockLocation, want)
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

func TestWindowsRegistrationNamespaceIdentityUsesRegistryScope(t *testing.T) {
	for _, level := range []ConfigLevel{ConfigUser, ConfigSystem} {
		first, firstDir, firstRegistration, err := registrationNamespaceLockSpec(Config{Level: level, Location: filepath.Join(t.TempDir(), "payload-a")}, filepath.Join(t.TempDir(), "payload-a"))
		if err != nil {
			t.Fatal(err)
		}
		second, secondDir, secondRegistration, err := registrationNamespaceLockSpec(Config{Level: level, Location: filepath.Join(t.TempDir(), "payload-b")}, filepath.Join(t.TempDir(), "payload-b"))
		if err != nil {
			t.Fatal(err)
		}
		if first != second || firstDir != secondDir || firstRegistration != secondRegistration {
			t.Fatalf("%s locations split a registry namespace: (%q,%q,%q) != (%q,%q,%q)", level, first, firstDir, firstRegistration, second, secondDir, secondRegistration)
		}
	}
	user, _, _, err := registrationNamespaceLockSpec(Config{Level: ConfigUser}, ConfigUser.ConfigLocation())
	if err != nil {
		t.Fatal(err)
	}
	system, _, _, err := registrationNamespaceLockSpec(Config{Level: ConfigSystem}, ConfigSystem.ConfigLocation())
	if err != nil {
		t.Fatal(err)
	}
	if user == system {
		t.Fatal("registry-user and registry-system share a namespace identity")
	}
	firstFileLocation := t.TempDir()
	firstFile, _, _, err := registrationNamespaceLockSpec(Config{Level: ConfigEnv}, firstFileLocation)
	if err != nil {
		t.Fatal(err)
	}
	secondFile, _, _, err := registrationNamespaceLockSpec(Config{Level: ConfigEnv}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if firstFile == secondFile || firstFile == user || firstFile == system {
		t.Fatal("file registration namespace collides with a separate namespace")
	}
}

func TestWindowsFileRegistrationNamespaceSymlinkAliasesShareLock(t *testing.T) {
	real := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	cfg := Config{Level: ConfigEnv}
	first, firstLocation, err := acquireRegistrationNamespaceLock(context.Background(), cfg, real, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()
	identity, _, registrationLocation, err := registrationNamespaceLockSpec(cfg, alias)
	if err != nil {
		t.Fatal(err)
	}
	firstIdentity, _, firstRegistrationLocation, err := registrationNamespaceLockSpec(cfg, real)
	if err != nil {
		t.Fatal(err)
	}
	if identity != firstIdentity || registrationLocation != firstLocation || firstRegistrationLocation != firstLocation {
		t.Fatalf("real path and alias resolved differently: %q/%q, %q/%q", firstIdentity, firstLocation, identity, registrationLocation)
	}
	second, _, err := acquireRegistrationNamespaceLock(context.Background(), cfg, alias, 20*time.Millisecond)
	if !errors.Is(err, fslock.ErrLockContended) {
		if second != nil {
			_ = second.release()
		}
		t.Fatalf("real-path/alias namespace lock error = %v, want contention", err)
	}
	firstDriverLock, err := driverInstallLockPath(real, "driver")
	if err != nil {
		t.Fatal(err)
	}
	aliasDriverLock, err := driverInstallLockPath(alias, "driver")
	if err != nil {
		t.Fatal(err)
	}
	if firstDriverLock == aliasDriverLock {
		t.Fatal("expected existing driver lock path to differ through a symlink alias")
	}
}

func TestWindowsRegistryCleanupRootRequiresMatchingTransactionReceipt(t *testing.T) {
	cfg := Config{Level: ConfigUser}
	root := t.TempDir()
	id := "driver-root-check"
	generationName := ".dbc-package-" + id + "-generation"
	generation := filepath.Join(root, generationName)
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generation, "driver.dll"), []byte("library"), 0o600); err != nil {
		t.Fatal(err)
	}
	info := DriverInfo{
		ID:      id,
		Name:    "Root check",
		Source:  "dbc",
		Version: semver.MustParse("1.2.3"),
	}
	info.Driver.Shared.Set(PlatformTuple(), filepath.Join(generation, "driver.dll"))
	manifest := Manifest{DriverInfo: info}
	manifest.Files.Driver = "driver.dll"
	stage := t.TempDir()
	if err := os.WriteFile(filepath.Join(stage, "driver.dll"), []byte("library"), 0o600); err != nil {
		t.Fatal(err)
	}
	receipt, err := makePackageInstallReceipt(cfg, stage, generationName, id, PlatformTuple(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePackageInstallReceipt(generation, receipt); err != nil {
		t.Fatal(err)
	}
	got, err := packageCleanupRootForSharedHelper(cfg, info)
	if err != nil {
		t.Fatalf("infer root for file inside valid generation: %v", err)
	}
	if got != root {
		t.Fatalf("inferred root = %q, want %q", got, root)
	}

	// A directory-valued shared reference can identify the generation itself.
	directoryInfo := info
	directoryInfo.Driver.Shared.Set(PlatformTuple(), generation)
	got, err = packageCleanupRootForSharedHelper(cfg, directoryInfo)
	if err != nil || got != root {
		t.Fatalf("infer root for directory-valued generation: got %q, err %v", got, err)
	}

	badReceipt := receipt
	badReceipt.RuntimeID = "another-driver"
	if err := os.Remove(filepath.Join(generation, packageInstallReceiptFilename)); err != nil {
		t.Fatal(err)
	}
	if err := writePackageInstallReceipt(generation, badReceipt); err != nil {
		t.Fatal(err)
	}
	if _, err := packageCleanupRootForSharedHelper(cfg, info); err == nil {
		t.Fatal("root inference accepted a receipt for another runtime ID")
	}
}
