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
	"path/filepath"
	"sync"
	"time"
)

type processLock struct {
	token chan struct{}
	refs  int
}

var processLocks = struct {
	sync.Mutex
	byPath map[string]*processLock
}{byPath: make(map[string]*processLock)}

func acquireContext(ctx context.Context, path string, timeout time.Duration) (Lock, error) {
	if err := ctx.Err(); err != nil {
		return Lock{}, err
	}
	canonicalPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return Lock{}, fmt.Errorf("fslock: resolve %s: %w", path, err)
	}

	processLocks.Lock()
	entry := processLocks.byPath[canonicalPath]
	if entry == nil {
		entry = &processLock{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		processLocks.byPath[canonicalPath] = entry
	}
	entry.refs++
	processLocks.Unlock()

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
		processLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(processLocks.byPath, canonicalPath)
		}
		processLocks.Unlock()
		if err := ctx.Err(); err != nil {
			return Lock{}, err
		}
		return Lock{}, fmt.Errorf("fslock: could not acquire lock on %s within %s: %w", path, timeout, ErrLockContended)
	}
	if err := ctx.Err(); err != nil {
		entry.token <- struct{}{}
		processLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(processLocks.byPath, canonicalPath)
		}
		processLocks.Unlock()
		return Lock{}, err
	}

	var once sync.Once
	return Lock{releaseFn: func() error {
		once.Do(func() {
			processLocks.Lock()
			entry.token <- struct{}{}
			entry.refs--
			if entry.refs == 0 {
				delete(processLocks.byPath, canonicalPath)
			}
			processLocks.Unlock()
		})
		return nil
	}}, nil
}

// Release releases a process-local lock. JavaScript/Wasm locks coordinate only
// callers in this process; they do not provide cross-process file locking.
func (l Lock) Release() error {
	if l.releaseFn == nil {
		return nil
	}
	return l.releaseFn()
}
