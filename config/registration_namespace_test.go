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

package config

import "testing"

func TestFileRegistrationNamespaceIdentityCaseFolding(t *testing.T) {
	path := `C:/Program Files/ADBC/Drivers`
	if got, want := fileRegistrationNamespaceIdentity(path, true), "file:c:/program files/adbc/drivers"; got != want {
		t.Fatalf("Windows namespace identity = %q, want %q", got, want)
	}
	if got, want := fileRegistrationNamespaceIdentity(path, false), "file:"+path; got != want {
		t.Fatalf("case-sensitive namespace identity = %q, want %q", got, want)
	}
}
