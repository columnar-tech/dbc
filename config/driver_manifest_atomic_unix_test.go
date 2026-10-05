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
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func TestCreateManifestAtomicallyReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "driver.toml")
	if err := os.WriteFile(path, []byte("old manifest"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	version, err := semver.NewVersion("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	driver := DriverInfo{ID: "driver", Name: "new manifest", Version: version}
	if err := CreateManifest(Config{Level: ConfigEnv, Location: dir}, driver); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("manifest was modified in place instead of replaced")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "old manifest" {
		t.Fatal("manifest content was not replaced")
	}
	if got := after.Mode().Perm(); got != 0o600 {
		t.Fatalf("replacement permission bits = %o, want 600", got)
	}
}

func TestCreateManifestHonorsRestrictiveUmask(t *testing.T) {
	const childEnv = "DBC_CONFIG_MANIFEST_UMASK_CHILD"
	if os.Getenv(childEnv) == "1" {
		syscall.Umask(0o077)
		dir := os.Getenv("DBC_CONFIG_MANIFEST_UMASK_PATH")
		version, err := semver.NewVersion("1.2.3")
		if err != nil {
			t.Fatal(err)
		}
		driver := DriverInfo{ID: "driver", Name: "new manifest", Version: version}
		if err := CreateManifest(Config{Level: ConfigEnv, Location: dir}, driver); err != nil {
			t.Fatal(err)
		}
		return
	}

	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCreateManifestHonorsRestrictiveUmask$")
	cmd.Env = append(os.Environ(), childEnv+"=1", "DBC_CONFIG_MANIFEST_UMASK_PATH="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("umask subprocess failed: %v\n%s", err, output)
	}
	info, err := os.Stat(filepath.Join(dir, "driver.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("new manifest permission bits = %o, want 600 under umask 077", got)
	}
}
