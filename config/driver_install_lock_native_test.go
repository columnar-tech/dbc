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
	"testing"
	"time"
)

func TestNativeDriverInstallLockUsesRuntimeIDFilename(t *testing.T) {
	root := t.TempDir()
	path, err := driverInstallLockPath(root, "driver")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".dbc.install-locks", "driver.lock")
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
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("driver lock directory was not retained: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lock file remains: %v", err)
	}
}
