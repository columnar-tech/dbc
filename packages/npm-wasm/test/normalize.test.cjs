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

const assert = require("assert");
const path = require("path");
const { normalizeLocation } = require("../index.cjs");
const {
  createGoFSAdapter,
  curateGoEnv,
  goPathToWindowsFS,
  windowsPathToGo,
} = require("../boot.cjs");

const HOST_PLATFORM = process.platform;

function withPlatform(platform, fn) {
  const orig = Object.getOwnPropertyDescriptor(process, "platform");
  Object.defineProperty(process, "platform", { value: platform, configurable: true });
  try {
    fn();
  } finally {
    Object.defineProperty(process, "platform", orig);
  }
}

// POSIX: backslash is a legal filename character, so locations pass through
// unchanged (regression guard for roborev 6527).
withPlatform("linux", () => {
  for (const p of ["/tmp/drivers", "/tmp/adbc\\drivers", "drivers\\test", "C:\\drivers"]) {
    assert.strictEqual(normalizeLocation(p), p, `posix passthrough ${JSON.stringify(p)}`);
  }
});

// Windows: drive paths are encoded as Unix-absolute for Go js/wasm. Node-side
// paths are resolved before encoding, and the Go fs adapter decodes them later.
withPlatform("win32", () => {
  const cases = [
    ["C:\\drivers", "/C:/drivers"],
    ["C:/drivers", "/C:/drivers"],
    ["C:\\a\\b\\c", "/C:/a/b/c"],
    ["C:drivers", "/C:/drivers"],
    ["C:", "/C:/"],
    ["d:\\Lower", "/d:/Lower"],
    ["D:/a/dbc/.dbc-wasm-smoke-abc", "/D:/a/dbc/.dbc-wasm-smoke-abc"],
  ];
  for (const [input, want] of cases) {
    assert.strictEqual(normalizeLocation(input), want, `win32 ${JSON.stringify(input)}`);
  }
  assert.throws(() => normalizeLocation("\\\\server\\share\\drivers"), /UNC/);
  assert.throws(() => normalizeLocation("\\\\?\\D:\\drivers"), /UNC/);

  if (HOST_PLATFORM === "win32") {
    assert.strictEqual(normalizeLocation(".\\drivers"), `/${process.cwd().replace(/\\/g, "/")}/drivers`);
    assert.strictEqual(normalizeLocation("/tmp/drivers"), `/${path.win32.resolve("/tmp/drivers").replace(/\\/g, "/")}`);
  }
});

console.log("normalizeLocation: POSIX passthrough + Windows transform passed");

// curateGoEnv (roborev 6570): the Go js/wasm runtime is handed only the few env
// vars it reads, so a large host env can't overflow wasm_exec.js's argv/env cap;
// Windows %USERPROFILE%/%TEMP% map to $HOME/$TMPDIR (forward-slashed) because
// GOOS=js os.TempDir() reads only $TMPDIR (else /tmp, which is missing on Windows).
const ALLOWED_ENV_KEYS = ["HOME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"];

// Windows: USERPROFILE -> HOME, TEMP -> TMPDIR, backslashes -> slashes, and
// arbitrary host vars (PATH/FOO/APPDATA) are dropped to bound the env size.
{
  const win = curateGoEnv(
    {
      USERPROFILE: "C:\\Users\\me",
      TEMP: "C:\\Users\\me\\AppData\\Local\\Temp",
      TMP: "C:\\nope",
      PATH: "x".repeat(40000),
      FOO: "bar",
      APPDATA: "C:\\AppData",
    },
    "win32"
  );
  assert.strictEqual(win.HOME, "/C:/Users/me", "win HOME from USERPROFILE");
  assert.strictEqual(win.TMPDIR, "/C:/Users/me/AppData/Local/Temp", "win TMPDIR from TEMP");
  assert.deepStrictEqual(Object.keys(win).sort(), ["HOME", "TMPDIR"], "win env limited to mapped vars");
  for (const k of Object.keys(win)) assert(ALLOWED_ENV_KEYS.includes(k), `win unexpected key ${k}`);
}

// Windows: an explicit TMPDIR wins over TEMP/TMP.
{
  const win = curateGoEnv({ TMPDIR: "X:\\explicit", TEMP: "C:\\temp" }, "win32");
  assert.strictEqual(win.TMPDIR, "/X:/explicit", "win TMPDIR precedence");
}

// POSIX: HOME wins over USERPROFILE; XDG_* forwarded; PATH dropped; backslashes
// are NOT rewritten (legal filename chars on POSIX).
{
  const posix = curateGoEnv(
    {
      HOME: "/home/me",
      USERPROFILE: "C:\\x",
      TMPDIR: "/tmp",
      XDG_CONFIG_HOME: "/home/me/.config",
      PATH: "/usr/bin:/bin",
    },
    "linux"
  );
  assert.strictEqual(posix.HOME, "/home/me", "posix HOME");
  assert.strictEqual(posix.TMPDIR, "/tmp", "posix TMPDIR");
  assert.strictEqual(posix.XDG_CONFIG_HOME, "/home/me/.config", "posix XDG forwarded");
  assert(!("PATH" in posix), "posix PATH dropped");
  for (const k of Object.keys(posix)) assert(ALLOWED_ENV_KEYS.includes(k), `posix unexpected key ${k}`);
}

// POSIX backslash passthrough: a home path containing a backslash is not mangled.
assert.strictEqual(curateGoEnv({ HOME: "/home/a\\b" }, "linux").HOME, "/home/a\\b", "posix backslash passthrough");

// Windows path codec: drive roots and drive paths are converted between the
// Go js/wasm Unix-absolute spelling and Node's native Windows spelling. Other
// relative and POSIX paths are preserved.
assert.strictEqual(goPathToWindowsFS("/C:"), "C:/", "decode C drive root");
assert.strictEqual(goPathToWindowsFS("/D:/"), "D:/", "decode D drive root");
assert.strictEqual(goPathToWindowsFS("/D:/a/dbc"), "D:/a/dbc", "decode D drive path");
assert.strictEqual(goPathToWindowsFS("relative/path"), "relative/path", "preserve relative path");
assert.strictEqual(goPathToWindowsFS("/tmp/drivers"), "/tmp/drivers", "preserve POSIX path");
assert.strictEqual(windowsPathToGo("C:\\"), "/C:/", "encode C drive root");
assert.strictEqual(windowsPathToGo("D:\\a\\dbc"), "/D:/a/dbc", "encode D drive path");
assert.strictEqual(windowsPathToGo("..\\drivers"), "../drivers", "preserve relative target");
assert.strictEqual(windowsPathToGo("/tmp/drivers"), "/tmp/drivers", "preserve POSIX target");
assert.strictEqual(windowsPathToGo("\\\\server\\share\\driver"), "\\\\server\\share\\driver", "preserve unsupported UNC target spelling");

// The adapter converts only filesystem path arguments. File descriptors, data,
// options, and ordinary callbacks keep their original values.
{
  const calls = [];
  const names = [
    "open", "mkdir", "readdir", "stat", "lstat", "unlink", "rmdir", "chmod",
    "chown", "lchown", "utimes", "truncate", "readlink", "rename", "link", "symlink",
  ];
  const fakeFS = { constants: { sentinel: true } };
  for (const name of names) {
    fakeFS[name] = (...args) => {
      calls.push({ name, args });
      if (name === "readlink") args[1](null, "D:\\target\\driver", "extra");
      return name;
    };
  }
  const originalStat = fakeFS.stat;
  const adapter = createGoFSAdapter(fakeFS, "win32");
  assert.strictEqual(fakeFS.stat, originalStat, "adapter does not mutate original fs module");
  const callback = () => {};
  const path = "/D:/a/dbc";

  adapter.open(path, 17, 0o600, callback);
  adapter.mkdir(path, 0o700, callback);
  adapter.readdir(path, callback);
  adapter.stat(path, callback);
  adapter.lstat(path, callback);
  adapter.unlink(path, callback);
  adapter.rmdir(path, callback);
  adapter.chmod(path, 0o700, callback);
  adapter.chown(path, 1, 2, callback);
  adapter.lchown(path, 1, 2, callback);
  adapter.utimes(path, 3, 4, callback);
  adapter.truncate(path, 5, callback);
  let readlinkTarget;
  adapter.readlink(path, (error, target, extra) => {
    assert.strictEqual(error, null);
    readlinkTarget = target;
    assert.strictEqual(extra, "extra");
  });
  adapter.rename("/C:/from", "/D:/to", callback);
  adapter.link("/C:/from", "/D:/to", callback);
  adapter.symlink("/C:/target", "/D:/link", callback);

  assert.strictEqual(readlinkTarget, "/D:/target/driver", "encode absolute readlink target");
  assert.deepStrictEqual(calls.map(({ name }) => name), names);
  const twoPathArgs = {
    rename: ["C:/from", "D:/to"],
    link: ["C:/from", "D:/to"],
    symlink: ["C:/target", "D:/link"],
  };
  for (const { name, args } of calls) {
    if (name === "rename" || name === "link" || name === "symlink") {
      assert.deepStrictEqual(args.slice(0, 2), twoPathArgs[name], `${name} path conversion`);
      assert.strictEqual(args[2], callback, `${name} callback identity`);
    } else {
      assert.strictEqual(args[0], "D:/a/dbc", `${name} path conversion`);
      if (name !== "readlink") assert.strictEqual(args[args.length - 1], callback, `${name} callback identity`);
    }
  }
  assert.strictEqual(calls[0].args[1], 17, "open flags unchanged");
  assert.strictEqual(calls[0].args[2], 0o600, "open permissions unchanged");
  const data = new Uint8Array([1, 2, 3]);
  const writeCallback = () => {};
  fakeFS.write = (...args) => calls.push({ name: "write", args });
  const originalWrite = fakeFS.write;
  assert.strictEqual(adapter.write, originalWrite, "fd/data method is not wrapped");
  adapter.write(17, data, 1, 2, 3, writeCallback);
  assert.strictEqual(calls.at(-1).args[0], 17, "write fd unchanged");
  assert.strictEqual(calls.at(-1).args[1], data, "write data unchanged");
  assert.strictEqual(calls.at(-1).args[5], writeCallback, "write callback unchanged");
  const posixFS = createGoFSAdapter(fakeFS, "linux");
  assert.strictEqual(posixFS, fakeFS, "POSIX fs is not wrapped");
}

console.log("Windows Go fs path codec + adapter passed");

console.log("curateGoEnv: Windows TMPDIR/HOME mapping + POSIX passthrough + env bounded passed");
