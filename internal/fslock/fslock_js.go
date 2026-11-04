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
	"fmt"
	"sync"
	"time"
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
	// Symlink aliases of one directory share one key in this runtime's lock
	// registry.
	canonicalPath, err := canonicalLockPath(path)
	if err != nil {
		return Lock{}, err
	}

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
	// The runtime token only orders callers within this runtime; the marker
	// (see marker.go) orders separate runtimes and native dbc.
	release, err := acquireMarker(ctx, markerPath(canonicalPath), path, timeout, nil, nil)
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

// Release releases the lock: the runtime token and, where there is a
// filesystem, the marker file other Go/Wasm runtimes wait on.
func (l Lock) Release() error {
	if l.releaseFn == nil {
		return nil
	}
	return l.releaseFn()
}
