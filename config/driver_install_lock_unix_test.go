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

	"github.com/columnar-tech/dbc/internal/fslock"
)

func TestDriverInstallLockAliasesShareUnderlyingLock(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "root-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	first, err := acquireDriverInstallLockWith(context.Background(), root, "driver", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()

	_, err = acquireDriverInstallLockWith(context.Background(), alias, "driver", 20*time.Millisecond)
	if !errors.Is(err, fslock.ErrLockContended) {
		t.Fatalf("symlink alias lock error = %v, want contention", err)
	}
}

func TestDriverInstallLockSeparatesRoots(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	first, err := acquireDriverInstallLockWith(context.Background(), firstRoot, "driver", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()

	second, err := acquireDriverInstallLockWith(context.Background(), secondRoot, "driver", time.Second)
	if err != nil {
		t.Fatalf("same driver in separate root should use an independent lock: %v", err)
	}
	if err := second.release(); err != nil {
		t.Fatal(err)
	}
}
