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

//go:build !windows

package fslock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Acquire acquires an exclusive advisory lock on the file at path, retrying
// until timeout elapses. Returns an error if the lock cannot be acquired.
func Acquire(path string, timeout time.Duration) (Lock, error) {
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return Lock{}, fmt.Errorf("fslock: open %s: %w", path, err)
		}

		lock, err := lockFile(f, path, deadline)
		if err == nil {
			return lock, nil
		}
		f.Close()
		if errors.Is(err, errStaleInode) {
			if time.Now().Before(deadline) {
				// Previous holder unlinked the file between our open and
				// our flock; the path now refers to a different inode.
				// Reopen and try again within the remaining budget.
				continue
			}
			return Lock{}, fmt.Errorf("fslock: could not acquire lock on %s within %s: %w", path, timeout, err)
		}
		return Lock{}, err
	}
}

// errStaleInode signals that the opened fd refers to an inode that has been
// unlinked (or replaced) since we opened it — we need to reopen and retry.
var errStaleInode = errors.New("fslock: stale inode")

func lockFile(f *os.File, path string, deadline time.Time) (Lock, error) {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			// Confirm the path still points at our inode. If the previous
			// holder unlinked it (or it was replaced), the lock we just took
			// is on a dead inode and a new caller could lock the new file.
			if stale, serr := inodeIsStale(f, path); serr != nil {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				return Lock{}, fmt.Errorf("fslock: stat %s: %w", path, serr)
			} else if stale {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				return Lock{}, errStaleInode
			}
			return Lock{f: f, path: path}, nil
		}
		if time.Now().After(deadline) {
			return Lock{}, fmt.Errorf("fslock: could not acquire lock on %s: %w", path, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
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

// Release unlinks the lock file and releases the lock. Unlinking before
// closing ensures no new caller can open the same inode and take the lock
// while we still hold it; combined with the inode recheck in Acquire, this
// guarantees that a fresh file at the same path is never raced against a
// soon-to-be-deleted one.
func (l Lock) Release() error {
	if l.f == nil {
		return nil
	}
	rmErr := os.Remove(l.path)
	closeErr := l.f.Close()
	if closeErr != nil {
		return closeErr
	}
	if rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return rmErr
	}
	return nil
}
