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

// Package systempath provides the Windows system installation root.
package systempath

import "sync"

const defaultProgramFilesRoot = `C:\Program Files`

var (
	programFilesRootMu sync.RWMutex
	programFilesRoot   = defaultProgramFilesRoot
)

// ProgramFilesRoot returns the root used for system-level driver files.
func ProgramFilesRoot() string {
	programFilesRootMu.RLock()
	defer programFilesRootMu.RUnlock()
	return programFilesRoot
}

// SetProgramFilesRootForTests temporarily overrides the system driver root.
// Windows test binaries call this once before running tests and restore it
// after their test suite completes.
func SetProgramFilesRootForTests(root string) func() {
	programFilesRootMu.Lock()
	previous := programFilesRoot
	programFilesRoot = root
	programFilesRootMu.Unlock()

	return func() {
		programFilesRootMu.Lock()
		programFilesRoot = previous
		programFilesRootMu.Unlock()
	}
}
