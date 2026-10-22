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

// Package winregroot provides registry roots for dbc's Windows registration
// code. Keeping registry-root selection separate from filesystem-root
// selection lets tests isolate those two concerns independently. Overrides
// affect only dbc callers and do not change Windows system libraries' view.
package winregroot

import (
	"sync"

	"golang.org/x/sys/windows/registry"
)

var (
	// Setters are used by TestMain before tests and restored after they finish.
	// The mutex protects reads and writes of these variables; it does not make
	// changing roots while tests are using them safe.
	rootsMu    sync.RWMutex
	userRoot   = registry.CURRENT_USER
	systemRoot = registry.LOCAL_MACHINE
)

// UserRoot returns the root used for per-user ADBC registration.
func UserRoot() registry.Key {
	rootsMu.RLock()
	defer rootsMu.RUnlock()
	return userRoot
}

// SystemRoot returns the root used for machine-wide ADBC registration.
func SystemRoot() registry.Key {
	rootsMu.RLock()
	defer rootsMu.RUnlock()
	return systemRoot
}

// SetRootsForTests replaces the roots used by dbc's own registry accesses and
// returns a function that restores the previous pair. Windows system APIs keep
// using their normal registry view.
func SetRootsForTests(user, system registry.Key) func() {
	rootsMu.Lock()
	previousUser, previousSystem := userRoot, systemRoot
	userRoot, systemRoot = user, system
	rootsMu.Unlock()

	return func() {
		rootsMu.Lock()
		userRoot, systemRoot = previousUser, previousSystem
		rootsMu.Unlock()
	}
}
