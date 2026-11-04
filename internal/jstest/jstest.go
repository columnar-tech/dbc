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

// Package jstest helps tests that run under GOOS=js GOARCH=wasm in Node.
package jstest

import (
	"fmt"
	"syscall/js"

	"github.com/columnar-tech/dbc/config"
)

// SetHostPlatform sets the platform tuple from Node's process.platform and
// process.arch, as the npm package does before its first call (index.cjs
// hostPlatformTuple); without it the tuple is "unknown_wasm64" and no
// package matches.
func SetHostPlatform() {
	process := js.Global().Get("process")
	platform, arch := process.Get("platform").String(), process.Get("arch").String()
	osName := map[string]string{"linux": "linux", "darwin": "macos", "win32": "windows", "freebsd": "freebsd"}[platform]
	archName := map[string]string{"x64": "amd64", "arm64": "arm64", "ia32": "x86"}[arch]
	if osName == "" || archName == "" {
		panic(fmt.Sprintf("jstest: unsupported host platform %s/%s", platform, arch))
	}
	config.SetPlatformTupleOverride(osName + "_" + archName)
}
