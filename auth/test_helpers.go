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

import (
	"path/filepath"
	"sync"
	"testing"
)

func SetCredPathForTesting(path string) (restore func()) {
	orig := credPath
	credPath = path
	return func() {
		credPath = orig
	}
}

func ResetCredentialsForTesting() {
	loaded = sync.Once{}
	loadedCredentials = nil
	credentialErr = nil
}

func GetCredPathForTesting() string {
	return credPath
}

func resetCredState(t *testing.T) {
	origCredPath := credPath
	origLoadedCredentials := loadedCredentials
	t.Cleanup(func() {
		credPath = origCredPath
		loadedCredentials = origLoadedCredentials
		loaded = sync.Once{}
		credentialErr = nil
	})
	loaded = sync.Once{}
	loadedCredentials = nil
	credentialErr = nil
}

func withTempCredPath(t *testing.T) string {
	orig := credPath
	t.Cleanup(func() { credPath = orig })
	credPath = filepath.Join(t.TempDir(), "credentials.toml")
	return credPath
}
