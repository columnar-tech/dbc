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

package auth

import "sync"

// Test helpers to manipulate internal state for testing

// SetCredPathForTesting sets the credential path for testing purposes
func SetCredPathForTesting(path string) (restore func()) {
	orig := credPath
	credPath = path
	return func() {
		credPath = orig
	}
}

// ResetCredentialsForTesting resets the loaded credentials state for testing
func ResetCredentialsForTesting() {
	loaded = sync.Once{}
	loadedCredentials = nil
	credentialErr = nil
}

// GetCredPathForTesting returns the current credential path
func GetCredPathForTesting() string {
	return credPath
}
