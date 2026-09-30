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
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func acquireContext(ctx context.Context, path string, allowInitialAttempt bool) (Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Lock{}, fmt.Errorf("fslock: open %s: %w", path, err)
	}

	ol := new(windows.Overlapped)
	firstAttempt := true
	var lastLockErr error
	for {
		if !(firstAttempt && allowInitialAttempt) {
			if err := ctx.Err(); err != nil {
				f.Close()
				return Lock{}, retryContextError(path, err, lastLockErr)
			}
		}
		firstAttempt = false

		err = windows.LockFileEx(windows.Handle(f.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0, 1, 0, ol)
		if err == nil {
			return Lock{f: f, path: path}, nil
		}
		lastLockErr = err
		if err := waitForRetry(ctx); err != nil {
			f.Close()
			return Lock{}, retryContextError(path, err, lastLockErr)
		}
	}
}

// Release releases the lock and removes the lock file. On Windows, Go opens
// files without FILE_SHARE_DELETE, so os.Remove returns ERROR_SHARING_VIOLATION
// if another process still has the file open — making the delete inherently
// safe. We close first so our own handle doesn't block the remove, then
// swallow only ERROR_SHARING_VIOLATION (deferred cleanup) and ErrNotExist.
// Other errors, including ERROR_ACCESS_DENIED, propagate.
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
	// ERROR_SHARING_VIOLATION is the unambiguous "another handle is open
	// without FILE_SHARE_DELETE" case — cleanup is simply deferred to
	// whoever closes last. ERROR_ACCESS_DENIED is *not* safe to swallow:
	// Windows returns it for read-only attributes, ACL changes, or when
	// the path has been replaced with a directory, none of which should
	// be reported as a successful Release.
	return errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
