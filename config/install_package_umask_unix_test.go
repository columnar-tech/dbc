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
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The umask is process-global, so this test must not run in parallel.
func TestInstallPackageGenerationModeHonorsUmask(t *testing.T) {
	for _, tc := range []struct {
		umask int
		want  os.FileMode
	}{{0o077, 0o700}, {0o022, 0o755}} {
		old := syscall.Umask(tc.umask)
		location := t.TempDir()
		cfg := Config{Level: ConfigEnv, Location: location}
		archive := writeCustomPackageArchive(t, "name = \"Driver\"\nversion = \"1.0.0\"\n[Files]\ndriver = \"driver.so\"\n", packageFile("driver.so", "library"))
		manifest, err := InstallPackage(context.Background(), cfg, "driver", archive, InstallPackageOptions{})
		syscall.Umask(old)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Dir(manifest.Driver.Shared.Get(PlatformTuple())))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != tc.want {
			t.Fatalf("umask %#o: generation mode = %#o, want %#o", tc.umask, got, tc.want)
		}
	}
}
