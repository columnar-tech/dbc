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

package config

import (
	"context"
	"path/filepath"
	"sync"
)

var packageInstallLocksMu sync.Mutex
var packageInstallLocks = map[string]chan struct{}{}

func acquirePackageInstallLock(path string) (func(), error) {
	return acquirePackageInstallLockContext(context.Background(), path)
}

func acquirePackageInstallLockContext(ctx context.Context, path string) (func(), error) {
	key := filepath.Clean(path)
	packageInstallLocksMu.Lock()
	lock := packageInstallLocks[key]
	if lock == nil {
		lock = make(chan struct{}, 1)
		packageInstallLocks[key] = lock
	}
	packageInstallLocksMu.Unlock()

	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
