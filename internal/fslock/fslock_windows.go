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
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// Acquire acquires an exclusive advisory lock on the file at path, retrying
// until timeout elapses. Returns an error if the lock cannot be acquired.
func Acquire(path string, timeout time.Duration) (Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Lock{}, fmt.Errorf("fslock: open %s: %w", path, err)
	}

	ol := new(windows.Overlapped)
	deadline := time.Now().Add(timeout)
	for {
		err = windows.LockFileEx(windows.Handle(f.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0, 1, 0, ol)
		if err == nil {
			return Lock{f: f, path: path}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return Lock{}, fmt.Errorf("fslock: could not acquire lock on %s within %s: %w", path, timeout, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Release releases the lock and removes the lock file. On Windows, Go opens
// files without FILE_SHARE_DELETE, so os.Remove will fail with a sharing
// violation (or access-denied) if another process still has the file open
// — making the delete inherently safe. We close first so our own handle
// doesn't block the remove, then swallow only sharing-related failures.
func (l Lock) Release() error {
	if l.f == nil {
		return nil
	}
	if err := l.f.Close(); err != nil {
		return err
	}
	if err := os.Remove(l.path); err != nil && !isBenignRemoveErr(err) {
		return err
	}
	return nil
}

func isBenignRemoveErr(err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	// Another handle is open without FILE_SHARE_DELETE — cleanup is simply
	// deferred to whoever closes last. Any other filesystem error (ACLs,
	// I/O failure, etc.) should propagate.
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
