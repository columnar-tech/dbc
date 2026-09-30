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

//go:build !js

package fslock_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/columnar-tech/dbc/internal/fslock"
)

func TestAcquireAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	lock, err := fslock.Acquire(path, 5*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestAcquireTwiceSequential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	lock1, err := fslock.Acquire(path, 5*time.Second)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	lock1.Release()

	lock2, err := fslock.Acquire(path, 5*time.Second)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	lock2.Release()
}

func TestReleaseLockFileLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	lock, err := fslock.Acquire(path, 5*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("lock file missing while held: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	after, err := os.Stat(path)
	if runtime.GOOS == "windows" {
		if !os.IsNotExist(err) {
			t.Fatalf("Windows lock file still exists after Release: stat err=%v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("persistent Unix lock file missing after Release: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("Unix Release replaced or unlinked the persistent lock file")
	}
	if after.Size() != 0 {
		t.Fatalf("persistent lock file size = %d, want 0", after.Size())
	}
}

func TestConcurrentAcquireIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	const workers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		holding int
		maxSeen int
	)
	for range workers {
		wg.Go(func() {
			lock, err := fslock.Acquire(path, 10*time.Second)
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			mu.Lock()
			holding++
			if holding > maxSeen {
				maxSeen = holding
			}
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			holding--
			mu.Unlock()
			if err := lock.Release(); err != nil {
				t.Errorf("Release: %v", err)
			}
		})
	}
	wg.Wait()
	if maxSeen != 1 {
		t.Fatalf("mutual exclusion violated: saw %d concurrent holders", maxSeen)
	}
}

func TestAcquireTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	lock1, err := fslock.Acquire(path, 5*time.Second)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer lock1.Release()

	_, err = fslock.Acquire(path, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	// Verify the error type is ErrLockContended
	if !errors.Is(err, fslock.ErrLockContended) {
		t.Fatalf("timeout error must wrap ErrLockContended, got: %v", err)
	}
}

func TestAcquireContextCancellationWhileWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	held, err := fslock.Acquire(path, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := fslock.AcquireContext(ctx, path, 5*time.Second)
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("AcquireContext error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AcquireContext did not return after cancellation")
	}
}

func TestAcquireUnwritableDirIsNotContention(t *testing.T) {
	// Simulate a lock failure that's due to permissions instead of actual lock
	// contention by creating a read-only file and later trying to lock on it
	path := filepath.Join(t.TempDir(), "test.lock")
	if err := os.WriteFile(path, nil, 0o400); err != nil {
		t.Fatalf("seed lock file: %v", err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })

	// Skip if running with elevated privs
	if os.Geteuid() == 0 {
		t.Skip("running as root/elevated: mode bits do not deny access")
	}

	_, err := fslock.Acquire(path, 100*time.Millisecond)
	if err == nil {
		t.Skip("filesystem does not enforce the read-only bit for this user")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Skipf("expected a permission error from open, got: %v", err)
	}
	if errors.Is(err, fslock.ErrLockContended) {
		t.Fatalf("permission failure must not be classified as contention: %v", err)
	}
}
