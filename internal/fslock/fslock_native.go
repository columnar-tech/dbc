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

//go:build !js

package fslock

import (
	"context"
	"errors"
	"time"
)

// acquireContext takes the OS lock, which orders native processes and is
// released by the kernel if the holder dies, and then the marker that Go/Wasm
// runtimes take (see marker.go), so that native and Wasm holders exclude each
// other. Only the OS lock holder creates a native marker, so a native marker
// found while holding the OS lock was left by a dead holder and is removed at
// once instead of waiting for it to go stale.
func acquireContext(ctx context.Context, path string, timeout time.Duration) (Lock, error) {
	deadline := time.Now().Add(timeout)
	lock, err := acquireOSLock(ctx, path, timeout)
	if err != nil {
		return Lock{}, err
	}
	canonicalPath, err := canonicalLockPath(path)
	if err != nil {
		return Lock{}, errors.Join(err, lock.releaseOSLock())
	}
	release, err := acquireMarker(ctx, markerPath(canonicalPath), path, time.Until(deadline), nativeMarkerOwner, isNativeMarker)
	if err != nil {
		return Lock{}, errors.Join(err, lock.releaseOSLock())
	}
	lock.releaseFn = release
	return lock, nil
}

// Release removes the marker, then releases the OS lock, so no other native
// process can hold the OS lock while this process's marker still exists.
func (l Lock) Release() error {
	var err error
	if l.releaseFn != nil {
		err = l.releaseFn()
	}
	return errors.Join(err, l.releaseOSLock())
}
