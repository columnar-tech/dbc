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

// SetPlatformTupleOverride sets the host platform tuple (e.g. "linux_amd64").
// Required under GOARCH=wasm, where the detected tuple is "unknown_wasm64". It
// sets the package var directly so every reader observes it. Call once at
// startup before any install/uninstall.
func SetPlatformTupleOverride(tuple string) {
	platformTuple = tuple
}
