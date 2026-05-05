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
	"errors"
	"io/fs"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestIsBenignRemoveErr(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		benign bool
	}{
		{"sharing violation", &fs.PathError{Op: "remove", Err: windows.ERROR_SHARING_VIOLATION}, true},
		{"not exist", os.ErrNotExist, true},
		{"wrapped not exist", &fs.PathError{Op: "remove", Err: os.ErrNotExist}, true},
		{"access denied", &fs.PathError{Op: "remove", Err: windows.ERROR_ACCESS_DENIED}, false},
		{"generic io error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBenignRemoveErr(tc.err); got != tc.benign {
				t.Fatalf("isBenignRemoveErr(%v) = %v, want %v", tc.err, got, tc.benign)
			}
		})
	}
}
