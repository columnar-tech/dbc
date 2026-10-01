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

package fslock_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/columnar-tech/dbc/internal/fslock"
)

const subprocessLockModeEnv = "DBC_FSLOCK_TEST_MODE"
const subprocessLockPathEnv = "DBC_FSLOCK_TEST_PATH"

func TestUnixReleasePreservesExistingZeroByteLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	if err := os.WriteFile(path, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := fslock.Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire existing lock file: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("existing lock file was removed: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("existing lock file inode changed")
	}
	if after.Size() != 0 || after.Mode().Perm() != 0o640 {
		t.Fatalf("existing lock file changed: size=%d mode=%#o", after.Size(), after.Mode().Perm())
	}
}

func TestUnixPersistentLockCoordinatesProcessesAndCrashRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	lock, err := fslock.Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("initial Acquire: %v", err)
	}
	initialInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// A second process must contend on the same inode while this process holds it.
	runLockSubprocess(t, "contend", path)
	if err := lock.Release(); err != nil {
		t.Fatalf("initial Release: %v", err)
	}

	// Reacquisition in another process must reuse the persistent inode.
	runLockSubprocess(t, "acquire", path)
	reacquired, err := fslock.Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("parent reacquisition: %v", err)
	}
	if err := reacquired.Release(); err != nil {
		t.Fatalf("parent reacquisition Release: %v", err)
	}
	afterRelease, err := os.Stat(path)
	if err != nil {
		t.Fatalf("lock path missing after release: %v", err)
	}
	if !os.SameFile(initialInfo, afterRelease) {
		t.Fatal("reacquisition replaced the persistent lock inode")
	}

	// Process exit without Release closes its descriptor and lets flock recover.
	runLockSubprocess(t, "exit-holding", path)
	afterExit, err := os.Stat(path)
	if err != nil {
		t.Fatalf("lock path missing after holder process exit: %v", err)
	}
	if !os.SameFile(initialInfo, afterExit) {
		t.Fatal("crash recovery replaced the persistent lock inode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	recovered, err := fslock.AcquireContext(ctx, path, time.Second)
	if err != nil {
		t.Fatalf("Acquire after holder process exit: %v", err)
	}
	if err := recovered.Release(); err != nil {
		t.Fatalf("Release after crash recovery: %v", err)
	}
}

func TestUnixLockSubprocessHelper(t *testing.T) {
	mode := os.Getenv(subprocessLockModeEnv)
	if mode == "" {
		return
	}
	path := os.Getenv(subprocessLockPathEnv)
	switch mode {
	case "contend":
		_, err := fslock.Acquire(path, 150*time.Millisecond)
		if !errors.Is(err, fslock.ErrLockContended) {
			t.Fatalf("child Acquire error = %v, want lock contention", err)
		}
	case "acquire":
		lock, err := fslock.Acquire(path, time.Second)
		if err != nil {
			t.Fatalf("child Acquire: %v", err)
		}
		if err := lock.Release(); err != nil {
			t.Fatalf("child Release: %v", err)
		}
	case "exit-holding":
		if _, err := fslock.Acquire(path, time.Second); err != nil {
			t.Fatalf("child Acquire before exit: %v", err)
		}
		os.Exit(0)
	default:
		t.Fatalf("unknown subprocess mode %q", mode)
	}
}

func runLockSubprocess(t *testing.T, mode, path string) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestUnixLockSubprocessHelper$")
	command.Env = append(os.Environ(), subprocessLockModeEnv+"="+mode, subprocessLockPathEnv+"="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("lock subprocess (%s): %v\n%s", mode, err, output)
	}
}
