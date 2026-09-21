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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
)

func TestUninstallDriverUsesRegisteredEnvironmentPath(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	registered := filepath.Join(root, "registered")
	if err := os.Mkdir(registered, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := makeInstallArchive(t, "example", "1.0.0", "library.so", []byte("installed library"))
	file := writeInstallArchive(t, archive, "registered")
	_, err := InstallPackage(Config{Level: ConfigEnv, Location: registered}, "example", file, installExpected("example", "registered", archive), InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	info, err := loadDriverFromManifest(registered, "example")
	if err != nil {
		t.Fatal(err)
	}
	combined := Config{Level: ConfigEnv, Location: missing + string(os.PathListSeparator) + registered}
	lockLocation, err := driverInstallLockLocation(combined, info)
	if err != nil {
		t.Fatal(err)
	}
	wantLockLocation, _ := filepath.Abs(registered)
	if lockLocation != wantLockLocation {
		t.Fatalf("uninstall lock location = %q, want actual registration location %q", lockLocation, wantLockLocation)
	}
	if err := UninstallDriver(combined, info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall created the missing first path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(registered, "example.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registered manifest still exists: %v", err)
	}
}

func TestUninstallDriverEnvironmentPathSharesInstallLock(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := makeInstallArchive(t, "example", "1.0.0", "library.so", []byte("installed library"))
	file := writeInstallArchive(t, archive, "registered")
	_, err := InstallPackage(Config{Level: ConfigEnv, Location: second}, "example", file, installExpected("example", "registered", archive), InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	info, err := loadDriverFromManifest(second, "example")
	if err != nil {
		t.Fatal(err)
	}
	lockLocation, err := driverInstallLockLocation(Config{Level: ConfigEnv, Location: first + string(os.PathListSeparator) + second}, info)
	if err != nil {
		t.Fatal(err)
	}
	releaseLock, err := acquireDriverInstallLock(lockLocation, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- UninstallDriver(Config{Level: ConfigEnv, Location: first + string(os.PathListSeparator) + second}, info)
	}()
	select {
	case err := <-done:
		releaseLock()
		t.Fatalf("uninstall did not wait on the install lock for the registered path: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	releaseLock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UninstallDriver returned an error after lock release: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("UninstallDriver did not finish after lock release")
	}
}

func TestUninstallDriverRejectsStaleRegistrationAfterWaiting(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	oldDir := filepath.Join(root, "example_linux_amd64_v1.0.0")
	if runtime.GOOS == "darwin" {
		oldDir = filepath.Join(root, "example_macos_"+runtime.GOARCH+"_v1.0.0")
	} else {
		oldDir = filepath.Join(root, "example_"+PlatformTuple()+"_v1.0.0")
	}
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldLibrary := filepath.Join(oldDir, "old.so")
	if err := os.WriteFile(oldLibrary, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := DriverInfo{ID: "example", Name: "Old", Version: semver.MustParse("1.0.0"), Source: "dbc"}
	old.Driver.Shared.defaultPath = oldLibrary
	if err := CreateManifest(cfg, old); err != nil {
		t.Fatal(err)
	}
	stale, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	lockLocation, err := driverInstallLockLocation(cfg, stale)
	if err != nil {
		t.Fatal(err)
	}
	releaseLock, err := acquireDriverInstallLock(lockLocation, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- UninstallDriver(cfg, stale) }()
	select {
	case err := <-done:
		releaseLock()
		t.Fatalf("uninstall completed while the lock was held: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	newLibrary := filepath.Join(root, "new-generation", "new.so")
	if err := os.MkdirAll(filepath.Dir(newLibrary), 0o755); err != nil {
		releaseLock()
		t.Fatal(err)
	}
	if err := os.WriteFile(newLibrary, []byte("new"), 0o644); err != nil {
		releaseLock()
		t.Fatal(err)
	}
	updated := DriverInfo{ID: "example", Name: "New", Version: semver.MustParse("2.0.0"), Source: "dbc"}
	updated.Driver.Shared.defaultPath = newLibrary
	if err := CreateManifest(cfg, updated); err != nil {
		releaseLock()
		t.Fatal(err)
	}
	releaseLock()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "registration changed") {
			t.Fatalf("UninstallDriver error = %v, want stale-registration error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("UninstallDriver did not finish after lock release")
	}
	current, err := GetDriver(cfg, "example")
	if err != nil || current.Version.String() != "2.0.0" {
		t.Fatalf("new registration was not preserved: %#v, %v", current, err)
	}
	if data, err := os.ReadFile(newLibrary); err != nil || string(data) != "new" {
		t.Fatalf("new library was not preserved: %q, %v", data, err)
	}
}

func TestInstallPackageCleansLegacyGenerationOnlyAfterSuccessfulRegistration(t *testing.T) {
	for _, test := range []struct {
		name          string
		verify        func(string, Manifest) error
		registerError error
	}{
		{name: "successful registration"},
		{name: "verification failure", verify: func(string, Manifest) error { return errors.New("verification failed") }},
		{name: "registration failure", registerError: errors.New("registration failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			legacy := createLegacyInstall(t, root, "example", "1.0.0", "")
			register := func(cfg Config, driver DriverInfo) error {
				if test.registerError != nil {
					return test.registerError
				}
				return CreateManifest(cfg, driver)
			}
			err := installVersionTwo(t, Config{Level: ConfigEnv, Location: root}, "example", test.verify, register)
			if test.registerError != nil && !errors.Is(err, test.registerError) {
				t.Fatalf("InstallPackage error = %v, want registration error", err)
			}
			if test.verify != nil && (err == nil || !strings.Contains(err.Error(), "verification failed")) {
				t.Fatalf("InstallPackage error = %v, want verification error", err)
			}
			if test.verify == nil && test.registerError == nil && err != nil {
				t.Fatal(err)
			}
			_, statErr := os.Stat(legacy)
			if test.verify == nil && test.registerError == nil {
				if !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("successful install retained legacy generation: %v", statErr)
				}
			} else if statErr != nil {
				t.Fatalf("failed install removed legacy generation: %v", statErr)
			}
		})
	}
}

func TestInstallPackageRetainsUnprovenLegacyCandidates(t *testing.T) {
	for _, test := range []struct {
		name        string
		setup       func(t *testing.T, root, knownCandidate string) string
		newManifest bool
	}{
		{name: "external shared file", setup: func(t *testing.T, root, candidate string) string {
			t.Helper()
			if err := os.Mkdir(candidate, 0o755); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(root, "external", "old.so")
			if err := os.MkdirAll(filepath.Dir(external), 0o755); err != nil {
				t.Fatal(err)
			}
			return createLegacyInstall(t, root, "example", "1.0.0", external)
		}},
		{name: "symlink candidate", setup: func(t *testing.T, root, candidate string) string {
			t.Helper()
			target := filepath.Join(root, "outside")
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			shared := filepath.Join(target, "old.so")
			if err := os.WriteFile(shared, []byte("old library"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, candidate); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			return createLegacyInstall(t, root, "example", "1.0.0", shared)
		}},
		{name: "unknown directory name", setup: func(t *testing.T, root, candidate string) string {
			t.Helper()
			unknown := filepath.Join(root, "example-old-generation")
			if err := os.Mkdir(unknown, 0o755); err != nil {
				t.Fatal(err)
			}
			shared := filepath.Join(unknown, "old.so")
			if err := os.WriteFile(shared, []byte("old library"), 0o644); err != nil {
				t.Fatal(err)
			}
			return createLegacyInstall(t, root, "example", "1.0.0", shared)
		}},
		{name: "receipt candidate", setup: func(t *testing.T, root, candidate string) string {
			t.Helper()
			legacy := createLegacyInstall(t, root, "example", "1.0.0", "")
			if err := os.WriteFile(filepath.Join(legacy, installReceiptName), []byte("broken"), 0o600); err != nil {
				t.Fatal(err)
			}
			return legacy
		}},
		{name: "new manifest references legacy directory", setup: func(t *testing.T, root, candidate string) string {
			t.Helper()
			return createLegacyInstall(t, root, "example", "1.0.0", "")
		}, newManifest: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			candidate := filepath.Join(root, "example_"+PlatformTuple()+"_v1.0.0")
			legacy := test.setup(t, root, candidate)
			if test.newManifest {
				oldLibrary := filepath.Join(legacy, "old-library.so")
				archiveManifest := []byte(fmt.Sprintf(`manifest_version = 1
name = "Example Driver"
version = "2.0.0"

[Driver]
shared = %q
`, oldLibrary))
				archive := makeInstallArchiveWithEntries(t, installArchiveEntry{name: "MANIFEST", data: archiveManifest})
				file := writeInstallArchive(t, archive, "references-legacy")
				expected := installExpected("example", "new-source", archive)
				expected.Version = "2.0.0"
				_, err := InstallPackage(Config{Level: ConfigEnv, Location: root}, "example", file, expected, InstallOptions{})
				_ = file.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else if err := installVersionTwo(t, Config{Level: ConfigEnv, Location: root}, "example", nil, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(legacy); err != nil {
				t.Fatalf("unproven legacy candidate was removed: %v", err)
			}
		})
	}
}

func TestInstallIntoFirstEnvironmentPathPreservesLaterLegacyInstall(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := createLegacyInstall(t, second, "example", "1.0.0", "")
	if err := installVersionTwo(t, Config{Level: ConfigEnv, Location: first + string(os.PathListSeparator) + second}, "example", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("install into first path removed later legacy generation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(second, "example.toml")); err != nil {
		t.Fatalf("later runtime registration was removed: %v", err)
	}
}
