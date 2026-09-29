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
	"syscall"
	"testing"
)

func TestIsLockContention(t *testing.T) {
	for _, err := range []error{syscall.EWOULDBLOCK, syscall.EAGAIN} {
		if !isLockContention(err) {
			t.Errorf("isLockContention(%v) = false", err)
		}
	}
	if isLockContention(syscall.EIO) {
		t.Fatal("I/O error must not be classified as lock contention")
	}
}
