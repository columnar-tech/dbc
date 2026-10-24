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

//go:build !windows && !js

package fslock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func acquireContext(ctx context.Context, path string, timeout time.Duration) (Lock, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return Lock{}, err
		}
		// Mode 0600 means only same-user coordination is supported.
		// TODO: Define cross-user permissions and interrupted-writer recovery.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return Lock{}, fmt.Errorf("fslock: open %s: %w", path, err)
		}

		lock, err := lockFile(ctx, f, path, deadline)
		if err == nil {
			return lock, nil
		}
		f.Close()
		if errors.Is(err, errStaleInode) {
			if time.Now().Before(deadline) {
				// Defensive: Release never unlinks, but the file may be removed
				// externally. The path was replaced between our open and flock; the
				// acquired lock would protect a different inode from callers
				// that open the current path, so retry against that inode.
				// Reopen and try again within the remaining budget.
				continue
			}
			return Lock{}, fmt.Errorf("fslock: could not acquire lock on %s within %s (%v): %w",
				path, timeout, err, ErrLockContended)
		}
		return Lock{}, err
	}
}

// errStaleInode signals that the opened fd refers to an inode that has been
// unlinked (or replaced) since we opened it — we need to reopen and retry.
var errStaleInode = errors.New("fslock: stale inode")

func lockFile(ctx context.Context, f *os.File, path string, deadline time.Time) (Lock, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Lock{}, err
		}
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			// Confirm the path still points at our inode. If it was replaced,
			// the lock we just took would not protect callers opening the path.
			if stale, serr := inodeIsStale(f, path); serr != nil {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				return Lock{}, fmt.Errorf("fslock: stat %s: %w", path, serr)
			} else if stale {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				return Lock{}, errStaleInode
			}
			return Lock{f: f, path: path}, nil
		}
		if !isLockContention(err) {
			return Lock{}, fmt.Errorf("fslock: lock %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			return Lock{}, fmt.Errorf("fslock: could not acquire lock on %s (%v): %w",
				path, err, ErrLockContended)
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Lock{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func isLockContention(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func inodeIsStale(f *os.File, path string) (bool, error) {
	var fdStat syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &fdStat); err != nil {
		return false, err
	}
	var pathStat syscall.Stat_t
	if err := syscall.Stat(path, &pathStat); err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return true, nil
		}
		return false, err
	}
	return fdStat.Ino != pathStat.Ino || fdStat.Dev != pathStat.Dev, nil
}

// Release closes the lock file descriptor, releasing the advisory lock.
// The lock file is intentionally left in place: unlinking it would let a new
// caller create a different inode while another waiter still holds or is
// about to lock the old one. Leftover lock files are therefore expected.
func (l Lock) Release() error {
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}
