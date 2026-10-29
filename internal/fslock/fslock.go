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
// access to shared resources. Native platforms use operating-system file locks
// to coordinate across processes. JavaScript/Wasm locking is limited to one Go
// runtime; it does not coordinate separate Wasm instances or Node workers.
package fslock

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
)

// Lock represents an acquired advisory file lock.
type Lock struct {
	f         *os.File
	path      string
	releaseFn func() error
}

var ErrLockContended = errors.New("lock is held by another process")

func canonicalRuntimePath(path string, windows bool) string {
	if windows {
		return strings.ToLower(path)
	}
	return path
}

// AcquireContext waits for the lock until it is acquired, timeout elapses, or ctx is
// canceled. A non-positive timeout makes a single acquisition attempt.
func AcquireContext(ctx context.Context, path string, timeout time.Duration) (Lock, error) {
	return acquireContext(ctx, path, timeout)
}

// Acquire is the compatibility wrapper for callers without a context.
func Acquire(path string, timeout time.Duration) (Lock, error) {
	return AcquireContext(context.Background(), path, timeout)
}
