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

// Package hostenv reads environment variables from the host process at the
// time of the call. os.Getenv reads a copy of the environment that Go takes
// at startup on Unix and js/wasm; that copy only follows Go's own os.Setenv,
// so when dbc runs inside a C, Rust, Python, or Node host, later changes the
// host makes are invisible to it. Host entry points (capi, wasm) install a
// lookup that reads the host's live environment.
package hostenv

import "os"

var lookup = os.LookupEnv

// SetLookup replaces the lookup. Call it during program initialization only.
func SetLookup(f func(string) (string, bool)) { lookup = f }

// Getenv returns the host's current value of key, or "" if unset.
func Getenv(key string) string {
	v, _ := lookup(key)
	return v
}
