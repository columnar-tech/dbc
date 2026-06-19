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
  // Go's js/wasm env starts empty, but the runtime (GOOS=js) resolves
  // os.UserHomeDir/UserConfigDir/UserCacheDir from $HOME and $XDG_*, and install
  // staging uses the temp dir. Forward only those: wasm_exec.js caps the combined
  // argv+env size, and a full process.env — notably on Windows CI runners, whose
  // environment is large — overflows that cap, making go.run() throw before the
  // runtime starts. Windows exposes the home dir as %USERPROFILE%, so map it to
  // $HOME for the GOOS=js home-dir lookup.
  go.env = {};
  const home = process.env.HOME || process.env.USERPROFILE;
  if (home !== undefined) go.env.HOME = home;
  for (const k of ["XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "TMPDIR", "TEMP", "TMP"]) {
    if (process.env[k] !== undefined) go.env[k] = process.env[k];
  }

  const bytes = fs.readFileSync(path.join(__dirname, "dbc.wasm"));
  const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(instance); // registers the dbc* globals, then parks on select{}
  await new Promise((resolve) => setImmediate(resolve));

  if (typeof globalThis.dbcSearch !== "function") {
    throw new Error("runtime did not register its API");
  }
};
