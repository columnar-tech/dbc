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

package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeDriverInstallLockUsesRuntimeIDFilename(t *testing.T) {
	root := t.TempDir()
	path, err := driverInstallLockPath(root, "driver")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".driver.dbc")
	if path != want {
		t.Fatalf("driver lock path = %q, want %q", path, want)
	}
	if _, err := driverInstallLockPath(root, "../driver"); err == nil {
		t.Fatal("driver lock path accepted a runtime ID with path traversal")
	}

	lock, err := acquireDriverInstallLockWith(context.Background(), root, "driver", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".dbc.install-locks")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private lock directory unexpectedly exists: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lock file remains: %v", err)
	}
}

func TestNativeDriverInstallLockRejectsExistingCollisions(t *testing.T) {
	for _, kind := range []string{"nonempty file", "directory symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, ".driver.dbc")
			target := t.TempDir()
			if kind == "nonempty file" {
				if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlink creation unavailable: %v", err)
			}

			if _, err := acquireDriverInstallLockWith(context.Background(), root, "driver", 0); err == nil {
				t.Fatal("acquired lock through an existing data collision")
			}
			if kind == "nonempty file" {
				if data, err := os.ReadFile(path); err != nil || string(data) != "preserve" {
					t.Fatalf("collision data changed: %q, %v", data, err)
				}
			} else {
				info, err := os.Lstat(path)
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("collision symlink changed: %v, %v", info, err)
				}
				if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
					t.Fatalf("symlink target changed: entries=%v, err=%v", entries, err)
				}
			}
		})
	}
}

func TestNativeDriverInstallLockDoesNotConfuseRegistrationScanner(t *testing.T) {
	root := t.TempDir()
	registration := "manifest_version = 1\nname = \"Foo\"\nversion = \"1.0.0\"\nsource = \"external\"\n[Driver]\nshared = \"library.so\"\n"
	if err := os.WriteFile(filepath.Join(root, "foo.toml"), []byte(registration), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireDriverInstallLockWith(context.Background(), root, "foo", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	if _, err := os.Stat(filepath.Join(root, ".foo.dbc")); err != nil {
		t.Fatalf("native per-driver lock file is missing while held: %v", err)
	}
	shared, certain, err := collectFileRegistrationSharedMaps(root, "")
	if err != nil || !certain || len(shared) != 1 {
		t.Fatalf("registration scan with a held lock = %#v, certain=%t, err=%v", shared, certain, err)
	}
}

func TestNativeDriverInstallLockSupportsMaximumRegistrationFilename(t *testing.T) {
	root := t.TempDir()
	runtimeID := strings.Repeat("a", 250)
	registrationPath := filepath.Join(root, runtimeID+".toml")
	if err := os.WriteFile(registrationPath, nil, 0o600); err != nil {
		t.Fatalf("create maximum-length registration filename: %v", err)
	}
	lockPath, err := driverInstallLockPath(root, runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(filepath.Base(lockPath)) != len(filepath.Base(registrationPath)) {
		t.Fatalf("lock filename length = %d, registration filename length = %d", len(filepath.Base(lockPath)), len(filepath.Base(registrationPath)))
	}
	lock, err := acquireDriverInstallLockWith(context.Background(), root, runtimeID, time.Second)
	if err != nil {
		t.Fatalf("acquire maximum-length runtime ID lock: %v", err)
	}
	defer lock.release()
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("maximum-length lock file was not created: %v", err)
	}
}
