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

const path = require("path");
const bootRuntime = require("./boot.cjs");

const PLATFORM_MAP = { linux: "linux", darwin: "macos", win32: "windows", freebsd: "freebsd" };
const ARCH_MAP = { x64: "amd64", arm64: "arm64", ia32: "x86" };

const ERROR_PREFIX = "dbc-wasm: ";

// Every error this layer surfaces is namespaced with `dbc-wasm:` so consumers
// can attribute and string-match failures regardless of which backend (in-process
// or worker) or which Go-side call produced them.
function prefixError(e) {
  const msg = e && e.message ? e.message : String(e);
  if (msg.startsWith(ERROR_PREFIX)) return e instanceof Error ? e : new Error(msg);
  return new Error(ERROR_PREFIX + msg);
}

function hostPlatformTuple() {
  const os = PLATFORM_MAP[process.platform];
  const arch = ARCH_MAP[process.arch];
  if (!os || !arch) {
    throw new Error(`${ERROR_PREFIX}unsupported host platform ${process.platform}/${process.arch}`);
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
    try {
      await bootRuntime();
    } catch (e) {
      // Namespace the boot failure like every other error this layer surfaces.
      throw prefixError(e);
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

  let closed = false;
  let hasInstall = false;
  // Idempotently mark the client closed, failing both the init gate (`ready`) and
  // every in-flight RPC with `err`. Returns false if it was already closed, so the
  // first teardown wins and later events (e.g. an `exit` after an explicit
  // `close()`) become no-ops. Routing the single `closed` toggle, the `readyReject`
  // init gate, and the pending-rejection through one helper means every failure
  // path settles the same two channels at once: a failing worker can never hang
  // (something always rejects) nor double-settle (already-settled promises ignore
  // later rejects, and the `closed` guard short-circuits repeat calls).
  const markClosed = (err) => {
    if (closed) return false;
    closed = true;
    readyReject(err); // no-op if `ready` already resolved/rejected
    for (const p of pending.values()) p.reject(err);
    pending.clear();
    return true;
  };

  worker.on("message", (msg) => {
    if (msg.type === "ready") {
      hasInstall = msg.hasInstall === true;
      return readyResolve();
    }
    if (msg.type === "fatal") {
      // The worker runtime failed to initialize. markClosed surfaces it through
      // `ready`; the init try/catch below terminates the worker so it cannot park
      // on parentPort and keep the process alive.
      markClosed(new Error("dbc-wasm: " + msg.error));
      return;
    }
    const p = pending.get(msg.id);
    if (!p) return;
    pending.delete(msg.id);
    if (msg.ok) p.resolve(msg.value);
    else p.reject(prefixError(new Error(msg.error)));
  });
  worker.on("error", (e) => {
    markClosed(prefixError(e));
  });
  worker.on("exit", (code) => {
    // markClosed no-ops when close()/init-failure already tore down; in that
    // expected case `ready` has settled and there is nothing left to reject.
    markClosed(new Error(`dbc-wasm: worker exited unexpectedly (code ${code})`));
  });

  const call = (fn, args) => {
    if (closed) return Promise.reject(new Error("dbc-wasm: client is closed"));
    return new Promise((resolve, reject) => {
      const id = ++nextId;
      pending.set(id, { resolve, reject });
      worker.postMessage({ id, fn, args });
    });
  };

  let handle;
  try {
    await ready;
    await call("dbcSetPlatform", [opts.platform || hostPlatformTuple()]);
    const cfg = JSON.stringify({ baseURL: opts.baseURL || "", credential: opts.credential || null });
    handle = await call("dbcNewClient", [cfg]);
  } catch (e) {
    // Initialization failed after the Worker was created (unsupported host
    // platform, rejected dbcNewClient, or a worker fatal/error/exit). Terminate
    // the worker before propagating so a failed loadDbc() never leaves a live
    // wasm worker behind keeping the Node process alive.
    markClosed(e);
    await worker.terminate();
    throw e;
  }

  return buildClient({
    call,
    handle,
    hasInstall,
    close: async () => {
      if (!markClosed(new Error(`${ERROR_PREFIX}client is closed`))) return;
      await worker.terminate();
    },
  });
}

// Single source of truth for the public client shape. Both backends supply a
// `call(fn, args)` dispatcher and a `close()`; the object literal lives here only
// once so the two paths can never drift (consistent method set, error prefixing,
// and Promise<void> close semantics by construction).
function buildClient({ call, handle, hasInstall, close }) {
  const parse = async (fn, args) => JSON.parse(await call(fn, args));
  const api = {
    search: (pattern = "") => parse("dbcSearch", [handle, pattern]),
    resolve: (name, platform) => parse("dbcResolve", [handle, name, platform]),
    verifySignature: (lib, sig) => call("dbcVerify", [lib, sig]),
    close,
  };
  // install/uninstall/listInstalled require a filesystem-capable build; both
  // backends feature-detect identically so the surface is symmetric.
  if (hasInstall) {
    // dbcInstall takes the client handle (it needs the instance's registry
    // config); dbcUninstall/dbcList are pure filesystem ops and intentionally
    // do not — per-instance config does not apply to them.
    api.install = (name, location) => parse("dbcInstall", [handle, name, normalizeLocation(location)]);
    api.uninstall = async (name, location) => {
      await call("dbcUninstall", [name, normalizeLocation(location)]);
    };
    api.listInstalled = (location) => parse("dbcList", [normalizeLocation(location)]);
  }
  return api;
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
  let handle;
  try {
    handle = await globalThis.dbcNewClient(cfg);
  } catch (e) {
    // Prefix load-time client construction failures (e.g. an invalid credential
    // registryURL) so the in-process backend attributes errors the same way the
    // worker backend and the rest of this layer do.
    throw prefixError(e);
  }

  let closed = false;
  // In-process dispatcher: invoke the Go-registered global directly, prefixing
  // any thrown error so attribution matches the worker backend.
  const call = async (fn, args) => {
    if (closed) throw new Error(`${ERROR_PREFIX}client is closed`);
    const f = globalThis[fn];
    if (typeof f !== "function") throw new Error(`${ERROR_PREFIX}unknown wasm function: ${fn}`);
    try {
      return await f(...args);
    } catch (e) {
      throw prefixError(e);
    }
  };

  const api = buildClient({
    call,
    handle,
    hasInstall: typeof globalThis.dbcInstall === "function",
    // Guard against a double-free: a second close() (or one racing the
    // finalizer) must not call dbcCloseClient on an already-released handle.
    close: async () => {
      if (closed) return;
      closed = true;
      if (clientFinalizer) clientFinalizer.unregister(api);
      globalThis.dbcCloseClient(handle);
    },
  });
  if (clientFinalizer) clientFinalizer.register(api, handle, api);
  return api;
}

module.exports = { loadDbc, hostPlatformTuple, normalizeLocation };
