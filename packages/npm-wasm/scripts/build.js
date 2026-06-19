#!/usr/bin/env node
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

// build.js
//
// Builds the dbc WebAssembly module and assembles the @columnar-tech/dbc-wasm
// package: compiles ./wasm with -tags dbcnode, copies the matching wasm_exec.js
// from GOROOT, generates package.json at the given version, and copies LICENSE.
//
// Usage: node scripts/build.js [--version 0.3.0]

"use strict";

const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");

const PKG_DIR = path.resolve(__dirname, "..");
const REPO_ROOT = path.resolve(__dirname, "..", "..", "..");

function parseVersion() {
  const args = process.argv.slice(2);
  const i = args.indexOf("--version");
  return (i !== -1 ? args[i + 1] : "0.0.0-dev").replace(/^v/, "");
}

function buildWasm() {
  const out = path.join(PKG_DIR, "dbc.wasm");
  console.log("Building dbc.wasm (GOOS=js GOARCH=wasm -tags dbcnode)...");
  execFileSync("go", ["build", "-tags", "dbcnode", "-o", out, "./wasm"], {
    cwd: REPO_ROOT,
    env: { ...process.env, GOOS: "js", GOARCH: "wasm", GOWORK: "off" },
    stdio: "inherit",
  });
  console.log(`  -> ${out} (${fs.statSync(out).size} bytes)`);
}

function copyWasmExec() {
  const goroot = execFileSync("go", ["env", "GOROOT"]).toString().trim();
  const src = path.join(goroot, "lib", "wasm", "wasm_exec.js");
  const dest = path.join(PKG_DIR, "wasm_exec.js");
  fs.copyFileSync(src, dest);
  console.log(`  -> copied wasm_exec.js from ${src}`);
}

function writePackageJson(version) {
  const pkg = {
    name: "@columnar-tech/dbc-wasm",
    version,
    description: "WebAssembly build of dbc: search, install, and manage ADBC drivers from Node",
    keywords: ["adbc", "arrow", "database", "drivers", "dbc", "wasm", "webassembly"],
    homepage: "https://columnar.tech/dbc",
    bugs: "https://github.com/columnar-tech/dbc/issues",
    license: "Apache-2.0",
    repository: {
      type: "git",
      url: "https://github.com/columnar-tech/dbc.git",
      directory: "packages/npm-wasm",
    },
    type: "commonjs",
    main: "index.cjs",
    module: "index.mjs",
    types: "index.d.ts",
    exports: {
      ".": {
        types: "./index.d.ts",
        import: "./index.mjs",
        require: "./index.cjs",
      },
    },
    engines: { node: ">=18" },
    files: ["index.cjs", "index.mjs", "index.d.ts", "boot.cjs", "worker.cjs", "wasm_exec.js", "dbc.wasm", "README.md", "LICENSE"],
  };
  fs.writeFileSync(path.join(PKG_DIR, "package.json"), JSON.stringify(pkg, null, 2) + "\n");
  console.log(`  -> wrote package.json at version ${version}`);
}

function copyLicense() {
  fs.copyFileSync(path.join(REPO_ROOT, "LICENSE"), path.join(PKG_DIR, "LICENSE"));
}

function main() {
  const version = parseVersion();
  buildWasm();
  copyWasmExec();
  writePackageJson(version);
  copyLicense();
  console.log("\n@columnar-tech/dbc-wasm assembled.");
}

try {
  main();
} catch (err) {
  console.error(err.message);
  process.exit(1);
}
