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

//go:build !windows

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

func TestAcquireDriverInstallLockUsesRuntimeIdentity(t *testing.T) {
	location := t.TempDir()
	first, err := acquireDriverInstallLockWith(context.Background(), location, "MyDriver", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()
	_, err = acquireDriverInstallLockWith(context.Background(), location, "MyDriver", 20*time.Millisecond)
	if !errors.Is(err, fslock.ErrLockContended) {
		t.Fatalf("same-driver lock error = %v, want contention", err)
	}
	other, err := acquireDriverInstallLockWith(context.Background(), location, "other-driver", time.Second)
	if err != nil {
		t.Fatalf("different driver should use a separate lock: %v", err)
	}
	if err := other.release(); err != nil {
		t.Fatal(err)
	}

	original, err := driverInstallLockPath(location, "MyDriver")
	if err != nil {
		t.Fatal(err)
	}
	caseVariant, err := driverInstallLockPath(strings.ToUpper(location), "mydriver")
	if err != nil {
		t.Fatal(err)
	}
	if original == caseVariant {
		t.Fatalf("case-sensitive runtime lock paths unexpectedly match: %q", original)
	}
}

func TestFileRegistrationNamespaceUsesNormalizedDirectoryIdentity(t *testing.T) {
	location := t.TempDir()
	firstIdentity, firstDirectory, firstRegistration, err := registrationNamespaceLockSpec(Config{Level: ConfigEnv}, location)
	if err != nil {
		t.Fatal(err)
	}
	secondLocation := filepath.Join(location, "nested", "..")
	secondIdentity, secondDirectory, secondRegistration, err := registrationNamespaceLockSpec(Config{Level: ConfigEnv}, secondLocation)
	if err != nil {
		t.Fatal(err)
	}
	if firstIdentity != secondIdentity || firstDirectory != secondDirectory || firstRegistration != secondRegistration {
		t.Fatalf("normalized file namespace differs: (%q, %q, %q) != (%q, %q, %q)", firstIdentity, firstDirectory, firstRegistration, secondIdentity, secondDirectory, secondRegistration)
	}
	otherIdentity, _, _, err := registrationNamespaceLockSpec(Config{Level: ConfigEnv}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if otherIdentity == firstIdentity {
		t.Fatal("different file registration directories share a namespace")
	}
}

func TestFileRegistrationNamespaceSymlinkAliasesShareLock(t *testing.T) {
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
	secondIdentity, _, resolvedAlias, err := registrationNamespaceLockSpec(cfg, alias)
	if err != nil {
		t.Fatal(err)
	}
	firstIdentity, _, resolvedReal, err := registrationNamespaceLockSpec(cfg, real)
	if err != nil {
		t.Fatal(err)
	}
	if firstIdentity != secondIdentity || firstLocation != resolvedReal || firstLocation != resolvedAlias {
		t.Fatalf("symlink namespace mismatch: first=%q/%q alias=%q/%q", firstIdentity, firstLocation, secondIdentity, resolvedAlias)
	}
	second, _, err := acquireRegistrationNamespaceLock(context.Background(), cfg, alias, 20*time.Millisecond)
	if !errors.Is(err, fslock.ErrLockContended) {
		if second != nil {
			_ = second.release()
		}
		t.Fatalf("real-path/alias lock error = %v, want contention", err)
	}

	realDriverLock, err := driverInstallLockPath(real, "driver")
	if err != nil {
		t.Fatal(err)
	}
	aliasDriverLock, err := driverInstallLockPath(alias, "driver")
	if err != nil {
		t.Fatal(err)
	}
	if realDriverLock == aliasDriverLock {
		t.Fatal("expected existing driver lock path to differ through a symlink alias")
	}
}

func TestUninstallDriverRefusesChangedRegistration(t *testing.T) {
	location := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: location}
	oldPath := filepath.Join(location, "old.so")
	newPath := filepath.Join(location, "new.so")
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	selected := makeDriverInstallTestInfo("driver", location)
	selected.Source = "external"
	selected.Driver.Shared.Set("linux_amd64", oldPath)
	if err := CreateManifest(cfg, selected); err != nil {
		t.Fatal(err)
	}
	selected, err := GetDriver(cfg, selected.ID)
	if err != nil {
		t.Fatal(err)
	}

	replacement := makeDriverInstallTestInfo("driver", location)
	replacement.Version = semver.MustParse("2.0.0")
	replacement.Source = "external"
	replacement.Driver.Shared.Set("linux_amd64", newPath)
	if err := CreateManifest(cfg, replacement); err != nil {
		t.Fatal(err)
	}

	err = UninstallDriver(cfg, selected)
	if !errors.Is(err, errDriverRegistrationChanged) {
		t.Fatalf("UninstallDriver error = %v, want errDriverRegistrationChanged", err)
	}
	if _, err := os.Stat(filepath.Join(location, "driver.toml")); err != nil {
		t.Fatalf("replacement registration was removed: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("replacement library was removed: %v", err)
	}
}

func makeDriverInstallTestInfo(id, location string) DriverInfo {
	info := DriverInfo{
		ID:        id,
		FilePath:  location,
		Name:      "Test Driver",
		Publisher: "Test Publisher",
		License:   "MIT",
		Version:   semver.MustParse("1.2.3"),
		Source:    "external",
	}
	info.AdbcInfo.Version = semver.MustParse("1.1.0")
	info.Driver.Entrypoint = "AdbcDriverInit"
	info.Driver.Shared.Set(PlatformTuple(), filepath.Join(location, id, "driver.so"))
	return info
}
