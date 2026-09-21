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
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUninstallDriverWaitsForInstallLock(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := makeInstallArchive(t, "example", "1.0.0", "library.so", []byte("installed library"))
	file := writeInstallArchive(t, archive, "uninstall-lock")
	installed, err := InstallPackage(cfg, "example", file, installExpected("example", "uninstall-lock", archive), InstallOptions{})
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	info, err := GetDriver(cfg, "example")
	if err != nil {
		t.Fatal(err)
	}
	libraryPath := installed.Driver.Shared.Get(PlatformTuple())
	if data, err := os.ReadFile(libraryPath); err != nil || string(data) != "installed library" {
		t.Fatalf("installed library unavailable before uninstall: %q, %v", data, err)
	}
	location, err := driverInstallLockLocation(cfg, info)
	if err != nil {
		t.Fatal(err)
	}
	releaseLock, err := acquireDriverInstallLock(location, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			releaseLock()
		}
	}()

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- UninstallDriver(cfg, info)
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("uninstall completed while the install lock was held: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	_, manifestErr := os.Stat(filepath.Join(root, "example.toml"))
	libraryData, libraryErr := os.ReadFile(libraryPath)
	releaseLock()
	lockHeld = false
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UninstallDriver returned an error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("UninstallDriver did not complete after the install lock was released")
	}
	if manifestErr != nil {
		t.Fatalf("runtime manifest changed while the install lock was held: %v", manifestErr)
	}
	if libraryErr != nil || string(libraryData) != "installed library" {
		t.Fatalf("installed library changed while the install lock was held: %q, %v", libraryData, libraryErr)
	}
	if _, err := os.Stat(filepath.Join(root, "example.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime manifest still exists after uninstall: %v", err)
	}
	if _, err := os.Stat(libraryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installed library still exists after uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(libraryPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("receipt-owned generation directory remains after uninstall: %v", err)
	}
}

func TestDriverInstallLockLocation(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	registryPath := filepath.Join(root, "registry-fallback")
	for _, location := range []string{first, second, registryPath} {
		if err := os.MkdirAll(location, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		cfg  Config
		info DriverInfo
		want string
	}{
		{
			name: "environment uses actual registration location",
			cfg:  Config{Level: ConfigEnv, Location: first + string(os.PathListSeparator) + second},
			info: DriverInfo{ID: "example", FilePath: second},
			want: second,
		},
		{
			name: "filesystem manifest uses its actual location",
			cfg:  Config{Level: ConfigUser, Location: first},
			info: DriverInfo{ID: "example", FilePath: second},
			want: second,
		},
		{
			name: "registry manifest uses configured filesystem root",
			cfg:  Config{Level: ConfigUser, Location: registryPath},
			info: DriverInfo{ID: "example", FilePath: `HKCU\\SOFTWARE\\ADBC\\Drivers`},
			want: registryPath,
		},
		{
			name: "registry manifest defaults to config filesystem root",
			cfg:  Config{Level: ConfigUser},
			info: DriverInfo{ID: "example", FilePath: `HKCU\\SOFTWARE\\ADBC\\Drivers`},
			want: ConfigUser.ConfigLocation(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := driverInstallLockLocation(test.cfg, test.info)
			if err != nil {
				t.Fatal(err)
			}
			want, err := filepath.Abs(test.want)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("driverInstallLockLocation() = %q, want %q", got, want)
			}
		})
	}
}

func TestUninstallPackageCleanupLocation(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	registryPath := filepath.Join(root, "registry-fallback")
	for _, location := range []string{first, second, registryPath} {
		if err := os.MkdirAll(location, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		cfg  Config
		info DriverInfo
		want string
	}{
		{
			name: "environment cleans the registered manifest directory",
			cfg:  Config{Level: ConfigEnv, Location: first + string(os.PathListSeparator) + second},
			info: DriverInfo{ID: "example", FilePath: second},
			want: second,
		},
		{
			name: "filesystem manifest cleans its actual directory",
			cfg:  Config{Level: ConfigUser, Location: first},
			info: DriverInfo{ID: "example", FilePath: second},
			want: second,
		},
		{
			name: "registry manifest falls back to configured filesystem root",
			cfg:  Config{Level: ConfigUser, Location: registryPath},
			info: DriverInfo{ID: "example", FilePath: `HKCU\\SOFTWARE\\ADBC\\Drivers`},
			want: registryPath,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := uninstallPackageCleanupLocation(test.cfg, test.info)
			if err != nil {
				t.Fatal(err)
			}
			want, err := filepath.Abs(test.want)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("uninstallPackageCleanupLocation() = %q, want %q", got, want)
			}
		})
	}
}
