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

"use strict";

const fs = require("fs");
const path = require("path");

// Single source of truth for instantiating the Go wasm runtime under Node. Both
// backends (in-process in index.cjs and the worker thread in worker.cjs) boot
// the module identically; any change to the boot protocol (a new global to
// inject, a Node-version polyfill, instantiateStreaming) must apply to both, so
// it lives here exactly once. Callers keep their own error-reporting policy: the
// in-process path lets the rejection propagate, the worker posts a `fatal`
// message — but both share this instantiation sequence by construction.
//
// Throws an unprefixed Error on failure; callers add the `dbc-wasm:` namespace.
module.exports = async function bootRuntime() {
  // The upstream wasm_exec.js is browser-oriented. Under Node we must supply
  // the real fs/process (and webcrypto on Node 18) BEFORE loading it, or Go's
  // filesystem syscalls return "not implemented on js".
  if (!globalThis.crypto) globalThis.crypto = require("crypto").webcrypto;
  if (!globalThis.fs) globalThis.fs = fs;
  if (!globalThis.process) globalThis.process = process;

  require("./wasm_exec.js"); // defines globalThis.Go

  const go = new globalThis.Go();
  go.env = process.env; // Go's js/wasm environment is empty by default ($HOME, $XDG_*)

  const bytes = fs.readFileSync(path.join(__dirname, "dbc.wasm"));
  const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(instance); // registers the dbc* globals, then parks on select{}
  await new Promise((resolve) => setImmediate(resolve));

  if (typeof globalThis.dbcSearch !== "function") {
    throw new Error("runtime did not register its API");
  }
};
