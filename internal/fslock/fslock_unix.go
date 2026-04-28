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
	"fmt"
	"os"
	"syscall"
	"time"
)

// Acquire acquires an exclusive advisory lock on the file at path, retrying
// until timeout elapses. Returns an error if the lock cannot be acquired.
func Acquire(path string, timeout time.Duration) (Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Lock{}, fmt.Errorf("fslock: open %s: %w", path, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return Lock{f: f}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return Lock{}, fmt.Errorf("fslock: could not acquire lock on %s within %s: %w", path, timeout, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
