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

const SINGLE_PATH_FS_METHODS = new Set([
  "open",
  "mkdir",
  "readdir",
  "stat",
  "lstat",
  "unlink",
  "rmdir",
  "chmod",
  "chown",
  "lchown",
  "utimes",
  "truncate",
  "readlink",
]);

const TWO_PATH_FS_METHODS = new Set(["rename", "link", "symlink"]);

function goPathToWindowsFS(value) {
  if (typeof value !== "string") return value;
  const match = /^\/([A-Za-z]:)(?:\/(.*))?$/.exec(value);
  if (!match) return value;
  return `${match[1]}/${match[2] || ""}`;
}

function goPathToPublicWindowsPath(value, platform) {
  if (platform !== "win32" || typeof value !== "string") return value;
  const decoded = goPathToWindowsFS(value);
  return decoded === value ? value : path.win32.normalize(decoded);
}

function restoreManifestPath(manifest, platform = process.platform) {
  if (!manifest || typeof manifest !== "object" || typeof manifest.driverPath !== "string") return manifest;
  const driverPath = goPathToPublicWindowsPath(manifest.driverPath, platform);
  return driverPath === manifest.driverPath ? manifest : { ...manifest, driverPath };
}

function restoreInstalledPaths(drivers, platform = process.platform) {
  if (platform !== "win32" || !Array.isArray(drivers)) return drivers;
  return drivers.map((driver) => {
    if (!driver || typeof driver !== "object" || typeof driver.filePath !== "string") return driver;
    const filePath = goPathToPublicWindowsPath(driver.filePath, platform);
    return filePath === driver.filePath ? driver : { ...driver, filePath };
  });
}

function windowsPathToGo(value) {
  if (typeof value !== "string") return value;
  const forwardSlashes = value.replace(/\\/g, "/");
  // UNC and device-namespace targets are outside the supported path contract;
  // preserve their spelling rather than turning them into drive paths.
  if (/^\/\//.test(forwardSlashes)) return value;
  if (/^[A-Za-z]:\//.test(forwardSlashes)) return `/${forwardSlashes}`;
  return forwardSlashes;
}

function createGoFSAdapter(fsModule, platform) {
  if (platform !== "win32") return fsModule;
  return new Proxy(fsModule, {
    get(target, property) {
      const method = Reflect.get(target, property, target);
      if (typeof method !== "function") return method;

      let pathIndexes;
      if (SINGLE_PATH_FS_METHODS.has(property)) pathIndexes = [0];
      else if (TWO_PATH_FS_METHODS.has(property)) pathIndexes = [0, 1];
      else return method;

      return (...args) => {
        for (const index of pathIndexes) args[index] = goPathToWindowsFS(args[index]);
        if (property === "readlink" && typeof args[1] === "function") {
          const callback = args[1];
          args[1] = function (error, targetPath, ...rest) {
            return callback.call(this, error, error ? targetPath : windowsPathToGo(targetPath), ...rest);
          };
        }
        return Reflect.apply(method, target, args);
      };
    },
  });
}

// curateGoEnv builds the minimal environment handed to the Go js/wasm runtime.
// wasm_exec.js caps the combined argv+env size; forwarding a full process.env
// (notably on Windows CI runners, whose environment is large) overflows that cap
// and makes go.run() throw before the runtime starts. The GOOS=js runtime reads
// only a few vars: $HOME (+ $XDG_*) for os.UserHomeDir/UserConfigDir/UserCacheDir,
// and $TMPDIR for os.TempDir() (it ignores %TEMP%/%TMP% and falls back to /tmp,
// which does not exist on a Windows host). Windows exposes the home/temp dirs as
// %USERPROFILE%/%TEMP%, so map them onto $HOME/$TMPDIR and encode Windows paths
// using the same Unix-absolute drive spelling as explicit API locations.
function curateGoEnv(env, platform) {
  const isWin = platform === "win32";
  const norm = (p) => {
    if (!isWin) return String(p);
    const windowsPath = String(p).replace(/\\/g, "/");
    if (/^(?:\\\\|\/\/)/.test(String(p))) {
      throw new Error("UNC and Windows device namespace paths are not supported by dbc-wasm");
    }
    const absolutePath = path.win32.resolve(windowsPath);
    if (/^\\\\/.test(absolutePath) || !/^[A-Za-z]:\\/.test(absolutePath)) {
      throw new Error(`could not resolve Windows environment path to an absolute drive path: ${String(p)}`);
    }
    return `/${absolutePath.replace(/\\/g, "/")}`;
  };
  const out = {};
  const home = env.HOME || env.USERPROFILE;
  if (home !== undefined) out.HOME = norm(home);
  const tmp = env.TMPDIR || env.TEMP || env.TMP;
  if (tmp !== undefined) out.TMPDIR = norm(tmp);
  for (const k of ["XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"]) {
    if (env[k] !== undefined) out[k] = env[k];
  }
  if (isWin) {
    for (const k of ["XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"]) {
      if (out[k] !== undefined && out[k] !== "") out[k] = norm(out[k]);
    }
  }
  return out;
}

// Single source of truth for instantiating the Go wasm runtime under Node. Both
// backends (in-process in index.cjs and the worker thread in worker.cjs) boot the
// module identically; any change to the boot protocol must apply to both, so it
// lives here exactly once. Callers keep their own error-reporting policy: the
// in-process path lets the rejection propagate, the worker posts a `fatal`
// message — but both share this instantiation sequence by construction.
//
// Throws an unprefixed Error on failure; callers add the `dbc-wasm:` namespace.
async function bootRuntime() {
  const goFS = createGoFSAdapter(globalThis.fs || fs, process.platform);

  // The upstream wasm_exec.js is browser-oriented. Under Node we must supply
  // the real fs/process (and webcrypto on Node 18) BEFORE loading it, or Go's
  // filesystem syscalls return "not implemented on js".
  if (!globalThis.crypto) globalThis.crypto = require("crypto").webcrypto;
  if (!globalThis.process) globalThis.process = process;

  // Keep the adapter as the Go runtime's global filesystem for its lifetime.
  // It forwards to the original module without mutating it.
  globalThis.fs = goFS;
  require("./wasm_exec.js"); // defines globalThis.Go

  const go = new globalThis.Go();
  go.env = curateGoEnv(process.env, process.platform);

  const bytes = fs.readFileSync(path.join(__dirname, "dbc.wasm"));
  const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
  go.run(instance); // registers the dbc* globals, then parks on select{}
  await new Promise((resolve) => setImmediate(resolve));

  if (typeof globalThis.dbcSearch !== "function") {
    throw new Error("runtime did not register its API");
  }
}

module.exports = bootRuntime;
module.exports.curateGoEnv = curateGoEnv;
module.exports.createGoFSAdapter = createGoFSAdapter;
module.exports.goPathToWindowsFS = goPathToWindowsFS;
module.exports.goPathToPublicWindowsPath = goPathToPublicWindowsPath;
module.exports.restoreManifestPath = restoreManifestPath;
module.exports.restoreInstalledPaths = restoreInstalledPaths;
module.exports.windowsPathToGo = windowsPathToGo;
