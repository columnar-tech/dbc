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
const { normalizeLocation } = require("../index.cjs");
const { curateGoEnv } = require("../boot.cjs");

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

// Windows: backslashes -> forward slashes; drive-relative -> absolute.
withPlatform("win32", () => {
  const cases = [
    ["/tmp/drivers", "/tmp/drivers"],
    ["C:\\drivers", "C:/drivers"],
    ["C:/drivers", "C:/drivers"],
    ["C:\\a\\b\\c", "C:/a/b/c"],
    ["C:drivers", "C:/drivers"],
    ["C:", "C:/"],
    ["d:\\Lower", "d:/Lower"],
  ];
  for (const [input, want] of cases) {
    assert.strictEqual(normalizeLocation(input), want, `win32 ${JSON.stringify(input)}`);
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
  assert.strictEqual(win.HOME, "C:/Users/me", "win HOME from USERPROFILE");
  assert.strictEqual(win.TMPDIR, "C:/Users/me/AppData/Local/Temp", "win TMPDIR from TEMP");
  assert.deepStrictEqual(Object.keys(win).sort(), ["HOME", "TMPDIR"], "win env limited to mapped vars");
  for (const k of Object.keys(win)) assert(ALLOWED_ENV_KEYS.includes(k), `win unexpected key ${k}`);
}

// Windows: an explicit TMPDIR wins over TEMP/TMP.
{
  const win = curateGoEnv({ TMPDIR: "X:\\explicit", TEMP: "C:\\temp" }, "win32");
  assert.strictEqual(win.TMPDIR, "X:/explicit", "win TMPDIR precedence");
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

console.log("curateGoEnv: Windows TMPDIR/HOME mapping + POSIX passthrough + env bounded passed");
