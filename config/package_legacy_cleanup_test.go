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
	"runtime"
	"syscall"
	"testing"
)

func TestUninstallLegacyInstallRemovesOnlyArchiveNamedPayload(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Level: ConfigEnv, Location: root}
	archive := writeCustomPackageArchive(t, "name = \"Legacy\"\nversion = \"1.0.0\"\n[Files]\ndriver = \"driver.so\"\n", packageFile("driver.so", "legacy"))
	archivePath := archive.Name()
	legacyArchivePath := filepath.Join(filepath.Dir(archivePath), "test-driver-1.tar.gz")
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(archivePath, legacyArchivePath); err != nil {
		t.Fatal(err)
	}
	downloaded, err := os.Open(legacyArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := InstallDriver(cfg, "driver", downloaded)
	if err != nil {
		t.Fatal(err)
	}
	generation := filepath.Dir(manifest.Driver.Shared.Get(PlatformTuple()))
	if filepath.Base(generation) != "test-driver-1" {
		t.Fatalf("legacy install directory = %q, want archive-derived basename", generation)
	}
	unrelated := filepath.Join(generation, "unrelated.txt")
	if err := os.WriteFile(unrelated, []byte("user data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CreateManifest(cfg, manifest.DriverInfo); err != nil {
		t.Fatal(err)
	}
	selected, err := GetDriver(cfg, "driver")
	if err != nil {
		t.Fatal(err)
	}
	if err := UninstallDriver(cfg, selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manifest.Driver.Shared.Get(PlatformTuple())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registered legacy payload remains: %v", err)
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "user data" {
		t.Fatalf("unrelated file changed: %q, %v", data, err)
	}
	if _, err := os.Stat(generation); err != nil {
		t.Fatalf("directory with unknown files was removed: %v", err)
	}
}

func TestLegacyCleanupPinsVerifiedDirectoryAcrossSymlinkSwap(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	probeDirectorySymlinkCapability(t, root, outside)

	generation := filepath.Join(root, "arbitrary-legacy-name")
	if err := os.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(generation, "driver.so")
	if err := os.WriteFile(library, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "renamed-legacy-directory")
	info := DriverInfo{ID: "driver", FilePath: root, Name: "Legacy", Source: "dbc"}
	info.Driver.Shared.Set(PlatformTuple(), library)
	operations := packageCleanupOperations{
		afterGenerationVerified: func(path string) {
			if path != generation {
				t.Errorf("verified path = %q, want %q", path, generation)
			}
			if err := os.Rename(generation, moved); err != nil {
				t.Errorf("rename verified generation: %v", err)
				return
			}
			if err := os.Symlink(outside, generation); err != nil {
				t.Errorf("replace generation path with symlink: %v", err)
			}
		},
	}
	if err := cleanupLegacyPackageRegistration(root, info, nil, true, operations); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("outside sentinel changed: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(moved, "driver.so")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verified original payload was not removed through its pinned handle: %v", err)
	}
	if info, err := os.Lstat(generation); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("replacement generation symlink = %v, %v", info, err)
	}
}

func probeDirectorySymlinkCapability(t *testing.T, root, target string) {
	t.Helper()
	probe := filepath.Join(root, "symlink-capability-probe")
	if err := os.Symlink(target, probe); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
			t.Skipf("directory symlinks require an unavailable Windows privilege: %v", err)
		}
		t.Fatalf("probe directory symlink support: %v", err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatalf("remove directory symlink capability probe: %v", err)
	}
}
