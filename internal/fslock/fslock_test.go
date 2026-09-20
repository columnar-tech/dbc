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

package fslock_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestReleaseRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	lock, err := fslock.Acquire(path, 5*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file missing while held: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lock file still on disk after Release: stat err=%v", err)
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

func TestAcquireContextCanceledWhileContended(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	held, err := fslock.Acquire(path, 5*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer held.Release()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := fslock.AcquireContext(ctx, path)
		done <- err
	}()

	start := time.Now()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("AcquireContext error = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
			t.Fatalf("AcquireContext took %s to observe cancellation", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("AcquireContext did not return after cancellation")
	}
}

func TestAcquireContextPreCanceledDoesNotCreateLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fslock.AcquireContext(ctx, path)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireContext error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-canceled AcquireContext left a lock file: stat error = %v", err)
	}
}

func TestAcquireContextPreDeadlineDoesNotCreateLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := fslock.AcquireContext(ctx, path)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcquireContext error = %v, want context.DeadlineExceeded", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired AcquireContext left a lock file: stat error = %v", err)
	}
}

func TestAcquireZeroTimeoutStillTriesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	lock, err := fslock.Acquire(path, 0)
	if err != nil {
		t.Fatalf("Acquire with zero timeout: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestAcquireContextDeadlineWhileContended(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	held, err := fslock.Acquire(path, 5*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer held.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = fslock.AcquireContext(ctx, path)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AcquireContext error = %v, want context.DeadlineExceeded", err)
	}
}

func TestAcquireContextAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")
	held, err := fslock.Acquire(path, 5*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct {
		lock fslock.Lock
		err  error
	}, 1)
	go func() {
		lock, err := fslock.AcquireContext(ctx, path)
		done <- struct {
			lock fslock.Lock
			err  error
		}{lock: lock, err: err}
	}()

	time.Sleep(20 * time.Millisecond)
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("AcquireContext: %v", result.err)
		}
		if err := result.lock.Release(); err != nil {
			t.Fatalf("context lock Release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AcquireContext did not acquire after the prior lock was released")
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
