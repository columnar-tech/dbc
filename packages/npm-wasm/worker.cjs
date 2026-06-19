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

// Worker entry for `loadDbc({ worker: true })`: instantiates the Go wasm runtime
// in this worker thread and answers {id, fn, args} RPC messages from the main
// thread, so heavy tar/PGP/install work doesn't block the host event loop.

const { parentPort } = require("worker_threads");
const bootRuntime = require("./boot.cjs");

(async () => {
  try {
    await bootRuntime();
  } catch (e) {
    // The main thread surfaces this through `ready` and tears the worker down;
    // report it instead of letting the rejection crash the worker silently.
    parentPort.postMessage({ type: "fatal", error: e && e.message ? e.message : String(e) });
    return;
  }

  parentPort.on("message", async (msg) => {
    try {
      const fn = globalThis[msg.fn];
      if (typeof fn !== "function") throw new Error(`unknown wasm function: ${msg.fn}`);
      const value = await fn(...msg.args);
      parentPort.postMessage({ id: msg.id, ok: true, value });
    } catch (e) {
      parentPort.postMessage({ id: msg.id, ok: false, error: e && e.message ? e.message : String(e) });
    }
  });

  // Report optional-method availability so the main thread feature-detects the
  // worker backend exactly as it does the in-process one (symmetric API surface).
  parentPort.postMessage({ type: "ready", hasInstall: typeof globalThis.dbcInstall === "function" });
})();
