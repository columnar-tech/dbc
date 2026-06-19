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

const PLATFORM_MAP = { linux: "linux", darwin: "macos", win32: "windows", freebsd: "freebsd" };
const ARCH_MAP = { x64: "amd64", arm64: "arm64", ia32: "x86" };

function hostPlatformTuple() {
  const os = PLATFORM_MAP[process.platform];
  const arch = ARCH_MAP[process.arch];
  if (!os || !arch) {
    throw new Error(`dbc-wasm: unsupported host platform ${process.platform}/${process.arch}`);
  }
  return `${os}_${arch}`;
}

function normalizeLocation(loc) {
  // Only on Windows: backslash is a legal filename character on POSIX, so POSIX
  // locations must pass through untouched. On Windows, convert backslashes to
  // forward slashes (Go's js/wasm filepath uses Unix semantics; Node fs accepts
  // forward-slash drive paths) and make a drive-relative path absolute.
  if (process.platform !== "win32") return String(loc);
  let p = String(loc).replace(/\\/g, "/");
  p = p.replace(/^([A-Za-z]:)(?![/])/, "$1/");
  return p;
}

let runtimePromise;

function ensureRuntime() {
  if (runtimePromise) return runtimePromise;
  runtimePromise = (async () => {
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
      throw new Error("dbc-wasm: runtime did not register its API");
    }
    // Platform is a process-global host constant; set it once at init.
    try {
      globalThis.dbcSetPlatform(hostPlatformTuple());
    } catch {
      // Unsupported host: install/resolve default platform must be set via loadDbc({ platform }).
    }
  })();
  return runtimePromise;
}

const clientFinalizer =
  typeof FinalizationRegistry !== "undefined"
    ? new FinalizationRegistry((handle) => {
        try {
          globalThis.dbcCloseClient(handle);
        } catch {
          // runtime already torn down; nothing to release
        }
      })
    : null;

async function loadDbcWorker(opts) {
  const { Worker } = require("worker_threads");
  const worker = new Worker(path.join(__dirname, "worker.cjs"));

  const pending = new Map();
  let nextId = 0;
  let readyResolve, readyReject;
  const ready = new Promise((res, rej) => {
    readyResolve = res;
    readyReject = rej;
  });

  worker.on("message", (msg) => {
    if (msg.type === "ready") return readyResolve();
    if (msg.type === "fatal") return readyReject(new Error("dbc-wasm: " + msg.error));
    const p = pending.get(msg.id);
    if (!p) return;
    pending.delete(msg.id);
    if (msg.ok) p.resolve(msg.value);
    else p.reject(new Error(msg.error));
  });
  worker.on("error", (e) => {
    readyReject(e);
    for (const p of pending.values()) p.reject(e);
    pending.clear();
  });

  const call = (fn, args) =>
    new Promise((resolve, reject) => {
      const id = ++nextId;
      pending.set(id, { resolve, reject });
      worker.postMessage({ id, fn, args });
    });

  await ready;
  await call("dbcSetPlatform", [opts.platform || hostPlatformTuple()]);
  const cfg = JSON.stringify({ baseURL: opts.baseURL || "", credential: opts.credential || null });
  const handle = await call("dbcNewClient", [cfg]);

  const parse = (s) => JSON.parse(s);
  return {
    search: async (pattern = "") => parse(await call("dbcSearch", [handle, pattern])),
    resolve: async (name, platform) => parse(await call("dbcResolve", [handle, name, platform])),
    verifySignature: (lib, sig) => call("dbcVerify", [lib, sig]),
    install: async (name, location) => parse(await call("dbcInstall", [handle, name, normalizeLocation(location)])),
    uninstall: async (name, location) => {
      await call("dbcUninstall", [name, normalizeLocation(location)]);
    },
    listInstalled: async (location) => parse(await call("dbcList", [normalizeLocation(location)])),
    close: async () => {
      try {
        await call("dbcCloseClient", [handle]);
      } finally {
        await worker.terminate();
      }
    },
  };
}

async function loadDbc(opts = {}) {
  if (opts.worker) return loadDbcWorker(opts);
  await ensureRuntime();

  // platform is a process-global host constant; only override when explicit.
  if (opts.platform) globalThis.dbcSetPlatform(opts.platform);

  // Each instance gets its own Go-side client handle, so baseURL/credentials are
  // isolated between instances and refreshed tokens persist across calls.
  const cfg = JSON.stringify({
    baseURL: opts.baseURL || "",
    credential: opts.credential || null,
  });
  const handle = await globalThis.dbcNewClient(cfg);

  const parse = (s) => JSON.parse(s);
  const api = {
    search: async (pattern = "") => parse(await globalThis.dbcSearch(handle, pattern)),
    resolve: async (name, platform) => parse(await globalThis.dbcResolve(handle, name, platform)),
    verifySignature: (lib, sig) => globalThis.dbcVerify(lib, sig),
    close: () => globalThis.dbcCloseClient(handle),
  };
  if (typeof globalThis.dbcInstall === "function") {
    api.install = async (name, location) =>
      parse(await globalThis.dbcInstall(handle, name, normalizeLocation(location)));
    api.uninstall = async (name, location) => {
      await globalThis.dbcUninstall(name, normalizeLocation(location));
    };
    api.listInstalled = async (location) =>
      parse(await globalThis.dbcList(normalizeLocation(location)));
  }
  if (clientFinalizer) clientFinalizer.register(api, handle);
  return api;
}

module.exports = { loadDbc, hostPlatformTuple, normalizeLocation };
