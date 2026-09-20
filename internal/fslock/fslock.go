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

// Package fslock provides advisory file locking for coordinating exclusive
// access to shared resources across processes.
package fslock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// Lock represents an acquired advisory file lock.
type Lock struct {
	f    *os.File
	path string
}

var ErrLockContended = errors.New("lock is held by another process")

const retryInterval = 50 * time.Millisecond

// Acquire acquires an exclusive advisory lock on the file at path, retrying
// until timeout elapses. Returns an error if the lock cannot be acquired.
func Acquire(path string, timeout time.Duration) (Lock, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// The original timeout-based API always tried the lock once before
	// checking whether its deadline had elapsed. Preserve that behavior for
	// zero and negative timeouts.
	lock, err := acquireContext(ctx, path, true)
	if errors.Is(err, context.DeadlineExceeded) {
		return Lock{}, fmt.Errorf("fslock: could not acquire lock on %s within %s (%v): %w",
			path, timeout, err, ErrLockContended)
	}
	return lock, err
}

// AcquireContext acquires an exclusive advisory lock on the file at path,
// retrying until the lock is acquired or ctx is canceled. If ctx is already
// canceled, it returns immediately without opening or creating the lock file.
func AcquireContext(ctx context.Context, path string) (Lock, error) {
	if err := ctx.Err(); err != nil {
		return Lock{}, err
	}
	return acquireContext(ctx, path, false)
}

// retryContextError retains both the cancellation cause and the last lock
// syscall error for callers that need to diagnose a failed wait.
func retryContextError(path string, ctxErr, lockErr error) error {
	if lockErr == nil {
		return ctxErr
	}
	return fmt.Errorf("fslock: could not acquire lock on %s: %w (last lock attempt: %w)",
		path, ctxErr, lockErr)
}

// waitForRetry pauses between non-blocking lock attempts while remaining
// responsive to cancellation and deadlines.
func waitForRetry(ctx context.Context) error {
	timer := time.NewTimer(retryInterval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
