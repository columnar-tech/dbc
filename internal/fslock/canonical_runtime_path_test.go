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

import "testing"

func TestCanonicalRuntimePathCaseFolding(t *testing.T) {
	path := `C:/Users/Runner/Drivers/.dbc.install.lock`
	windowsPath := canonicalRuntimePath(path, true)
	if want := `c:/users/runner/drivers/.dbc.install.lock`; windowsPath != want {
		t.Fatalf("Windows canonical lock path = %q, want %q", windowsPath, want)
	}
	if upperPath := canonicalRuntimePath(`C:/USERS/RUNNER/DRIVERS/.DBC.INSTALL.LOCK`, true); windowsPath != upperPath {
		t.Fatalf("case variants have different Windows lock keys: %q != %q", windowsPath, upperPath)
	}
	if got := canonicalRuntimePath(path, false); got != path {
		t.Fatalf("case-sensitive canonical lock path = %q, want %q", got, path)
	}
	if got := canonicalRuntimePath(`C:/USERS/RUNNER/DRIVERS/.DBC.INSTALL.LOCK`, false); got == path {
		t.Fatalf("non-Windows canonical lock path unexpectedly folded case: %q", got)
	}
}
