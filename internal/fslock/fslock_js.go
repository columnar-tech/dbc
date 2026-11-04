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

//go:build js

package fslock

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/columnar-tech/dbc/internal/hostpath"
)

type runtimeLock struct {
	token chan struct{}
	refs  int
}

// runtimeLocks orders callers that share this Go/Wasm runtime; the marker
// file (acquireMarker) orders separate runtimes.
var runtimeLocks = struct {
	sync.Mutex
	byPath map[string]*runtimeLock
}{byPath: make(map[string]*runtimeLock)}

// dropRuntimeLockRef drops one reference and forgets the entry when unused.
func dropRuntimeLockRef(entry *runtimeLock, canonicalPath string) {
	runtimeLocks.Lock()
	defer runtimeLocks.Unlock()
	entry.refs--
	if entry.refs == 0 {
		delete(runtimeLocks.byPath, canonicalPath)
	}
}

func acquireContext(ctx context.Context, path string, timeout time.Duration) (Lock, error) {
	if err := ctx.Err(); err != nil {
		return Lock{}, err
	}
	canonicalPath, err := hostpath.Abs(hostpath.Clean(path))
	if err != nil {
		return Lock{}, fmt.Errorf("fslock: resolve %s: %w", path, err)
	}
	// The lock file often does not exist yet, so resolve its existing parent
	// and append the basename. This makes symlink aliases of the same directory
	// share one key in this runtime's lock registry.
	parent := hostpath.Dir(canonicalPath)
	resolvedParent, err := hostpath.EvalSymlinks(parent)
	if err != nil {
		return Lock{}, fmt.Errorf("fslock: resolve lock directory %s: %w", parent, err)
	}
	canonicalPath = hostpath.Join(resolvedParent, hostpath.Base(canonicalPath))
	canonicalPath = canonicalRuntimePath(canonicalPath, hostpath.IsWindows())

	runtimeLocks.Lock()
	entry := runtimeLocks.byPath[canonicalPath]
	if entry == nil {
		entry = &runtimeLock{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		runtimeLocks.byPath[canonicalPath] = entry
	}
	entry.refs++
	runtimeLocks.Unlock()

	acquired := false
	if timeout <= 0 {
		select {
		case <-entry.token:
			acquired = true
		default:
		}
	} else {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		case <-entry.token:
			acquired = true
		}
	}
	if !acquired {
		dropRuntimeLockRef(entry, canonicalPath)
		if err := ctx.Err(); err != nil {
			return Lock{}, err
		}
		return Lock{}, fmt.Errorf("fslock: could not acquire lock on %s within %s: %w", path, timeout, ErrLockContended)
	}
	if err := ctx.Err(); err != nil {
		entry.token <- struct{}{}
		dropRuntimeLockRef(entry, canonicalPath)
		return Lock{}, err
	}
	release, err := acquireMarker(ctx, canonicalPath+markerSuffix, path, timeout)
	if err != nil {
		entry.token <- struct{}{}
		dropRuntimeLockRef(entry, canonicalPath)
		return Lock{}, err
	}

	var once sync.Once
	return Lock{releaseFn: func() error {
		var err error
		once.Do(func() {
			err = release()
			entry.token <- struct{}{}
			dropRuntimeLockRef(entry, canonicalPath)
		})
		return err
	}}, nil
}

// The runtime token only orders callers within one Go/Wasm runtime. Separate
// runtimes (Node workers, other Node processes) share nothing but the
// filesystem, so the holder also creates a marker file with O_EXCL. While the
// lock is held a heartbeat refreshes the marker's mtime; a marker untouched for
// markerStaleAfter belongs to a runtime that died holding the lock and is
// removed. Native dbc locks with flock/LockFileEx, which Node cannot take, so
// this does not exclude the native CLI.
const (
	markerSuffix     = ".js"
	markerStaleAfter = 30 * time.Second
	markerHeartbeat  = markerStaleAfter / 3
	markerPollEvery  = 10 * time.Millisecond
)

func acquireMarker(ctx context.Context, marker, path string, timeout time.Duration) (release func() error, err error) {
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			f.Close()
			return holdMarker(marker), nil
		}
		if errors.Is(err, syscall.ENOSYS) {
			// No filesystem (the browser build): nothing outside this runtime
			// can contend, so the runtime token is the whole lock.
			return func() error { return nil }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("fslock: create %s: %w", marker, err)
		}
		if info, statErr := os.Stat(marker); statErr == nil && time.Since(info.ModTime()) > markerStaleAfter {
			// Re-check right before removing, to narrow the window in which a
			// new holder's marker could be mistaken for the stale one. If it
			// was refreshed meanwhile, wait like for any live holder.
			if again, err := os.Stat(marker); err == nil && again.ModTime().Equal(info.ModTime()) {
				if err := os.Remove(marker); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return nil, fmt.Errorf("fslock: remove stale lock marker %s: %w", marker, err)
				}
				continue
			}
		}
		if timeout <= 0 || time.Now().After(deadline) {
			return nil, fmt.Errorf("fslock: could not acquire lock on %s within %s: %w", path, timeout, ErrLockContended)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(markerPollEvery):
		}
	}
}

// holdMarker keeps marker fresh until the returned release removes it.
func holdMarker(marker string) func() error {
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(markerHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				os.Chtimes(marker, now, now)
			}
		}
	}()
	return func() error {
		close(stop)
		<-done
		if err := os.Remove(marker); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("fslock: remove %s: %w", marker, err)
		}
		return nil
	}
}

// Release releases the lock: the runtime token and, where there is a
// filesystem, the marker file other Go/Wasm runtimes wait on.
func (l Lock) Release() error {
	if l.releaseFn == nil {
		return nil
	}
	return l.releaseFn()
}
