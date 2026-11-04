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

//go:build windows

package fslock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// holdExclusively opens path with no sharing, as Windows does while another
// process's Release deletes the lock file, and closes it after d.
func holdExclusively(t *testing.T, path string, d time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(d)
		windows.CloseHandle(h)
	}()
	t.Cleanup(func() { <-done })
}

// A lock file that is briefly unopenable while a previous holder's Release
// deletes it is contention, not a failure: the waiter must keep trying.
func TestAcquireWaitsOutATransientlyUnopenableLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	holdExclusively(t, path, 200*time.Millisecond)

	lock, err := Acquire(path, 2*time.Second)
	if err != nil {
		t.Fatalf("Acquire while the lock file was briefly unopenable: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

// Past the timeout, the open error is reported, not hidden.
func TestAcquireReportsTheOpenErrorAfterTheTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	holdExclusively(t, path, time.Second)

	start := time.Now()
	_, err := Acquire(path, 0)
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("Acquire with timeout 0: err = %v, want ERROR_SHARING_VIOLATION", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Acquire with timeout 0 took %s", elapsed)
	}
}

// A lasting ERROR_ACCESS_DENIED is a permission failure, not a release in
// progress: it must be reported promptly even with a long lock timeout.
// Windows denies opening a directory for writing, which makes it lasting.
func TestAcquireReportsALastingAccessDeniedPromptly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err := Acquire(path, 10*time.Second)
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("Acquire of a lock path that is a directory: err = %v, want ERROR_ACCESS_DENIED", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("a lasting access denial took %s to report; the lock timeout was 10s", elapsed)
	}
}
