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

//go:build js && !dbcnode

package main

import "syscall/js"

// Browser entrypoint (GOOS=js GOARCH=wasm, without -tags dbcnode). It exposes
// only the filesystem-free API (search/resolve/verifySignature) via
// registerCommon().
//
// STATUS: experimental / not yet delivered. There is intentionally no browser
// loader, no npm `exports` entry, and no browser smoke test — the npm package
// (packages/npm-wasm) ships only the Node build. This seam exists so the browser
// target keeps compiling; finishing it (loader + tests + a published entry, and
// a browser .d.ts derived from packages/npm-wasm/index.d.ts, the single
// canonical type declaration) is a tracked future phase. Until then, treat the
// browser build as unsupported.
func main() {
	registerCommon()
	js.Global().Get("console").Call("log", "dbc-wasm (browser) ready")
	select {}
}
