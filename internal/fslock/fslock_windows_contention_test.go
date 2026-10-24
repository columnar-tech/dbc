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
	"testing"

	"golang.org/x/sys/windows"
)

func TestIsLockContention(t *testing.T) {
	if !isLockContention(windows.ERROR_LOCK_VIOLATION) {
		t.Fatal("ERROR_LOCK_VIOLATION must be classified as contention")
	}
	if isLockContention(windows.ERROR_ACCESS_DENIED) {
		t.Fatal("ERROR_ACCESS_DENIED must not be classified as contention")
	}
	if isLockContention(windows.ERROR_INVALID_HANDLE) {
		t.Fatal("unexpected syscall error must not be classified as contention")
	}
}
