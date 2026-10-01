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

	"github.com/columnar-tech/dbc/internal/hostpath"
)

type runtimeLock struct {
	token chan struct{}
	refs  int
}

// runtimeLocks coordinates callers that share this Go/Wasm runtime only.
// Separate Wasm instances and Node workers have independent registries.
var runtimeLocks = struct {
	sync.Mutex
	byPath map[string]*runtimeLock
}{byPath: make(map[string]*runtimeLock)}

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
		runtimeLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(runtimeLocks.byPath, canonicalPath)
		}
		runtimeLocks.Unlock()
		if err := ctx.Err(); err != nil {
			return Lock{}, err
		}
		return Lock{}, fmt.Errorf("fslock: could not acquire lock on %s within %s: %w", path, timeout, ErrLockContended)
	}
	if err := ctx.Err(); err != nil {
		entry.token <- struct{}{}
		runtimeLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(runtimeLocks.byPath, canonicalPath)
		}
		runtimeLocks.Unlock()
		return Lock{}, err
	}

	var once sync.Once
	return Lock{releaseFn: func() error {
		once.Do(func() {
			runtimeLocks.Lock()
			entry.token <- struct{}{}
			entry.refs--
			if entry.refs == 0 {
				delete(runtimeLocks.byPath, canonicalPath)
			}
			runtimeLocks.Unlock()
		})
		return nil
	}}, nil
}

// Release releases a lock local to this Go/Wasm runtime. It does not coordinate
// separate Wasm instances or Node workers.
func (l Lock) Release() error {
	if l.releaseFn == nil {
		return nil
	}
	return l.releaseFn()
}
