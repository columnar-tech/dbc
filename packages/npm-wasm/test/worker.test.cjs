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
const fs = require("fs");
const http = require("http");
const os = require("os");
const path = require("path");

const { loadDbc } = require("..");

const REPO_ROOT = path.resolve(__dirname, "..", "..", "..");
const indexData = fs.readFileSync(path.join(REPO_ROOT, "cmd/dbc/testdata/test_index.yaml"));
const tarData = fs.readFileSync(path.join(REPO_ROOT, "cmd/dbc/testdata/test-driver-1.tar.gz"));

const server = http.createServer((req, res) => {
  if (req.url.startsWith("/index.yaml")) {
    res.setHeader("Content-Type", "application/yaml");
    res.end(indexData);
  } else if (req.url.includes(".tar.gz")) {
    res.setHeader("Content-Type", "application/gzip");
    res.setHeader("Content-Length", String(tarData.length));
    res.end(tarData);
  } else {
    res.statusCode = 404;
    res.end("not found");
  }
});

function findFile(dir, suffix) {
  for (const entry of fs.readdirSync(dir, { recursive: true })) {
    if (entry.toString().endsWith(suffix)) return path.join(dir, entry.toString());
  }
  return null;
}

async function main() {
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const base = `http://127.0.0.1:${server.address().port}`;

  const dbc = await loadDbc({ worker: true, baseURL: base, platform: "linux_amd64" });

  const search = await dbc.search("");
  assert(search.drivers.length > 0, "search returned no drivers");

  const installDir = fs.mkdtempSync(path.join(os.tmpdir(), "dbc-wasm-worker-"));
  const manifest = await dbc.install("test-driver-1", installDir);
  assert(manifest.driverPath && fs.existsSync(manifest.driverPath), "installed driver missing on disk");

  const installed = await dbc.listInstalled(installDir);
  assert(installed.length === 1 && installed[0].id === "test-driver-1", "listInstalled mismatch");

  const so = findFile(installDir, ".so");
  const sig = findFile(installDir, ".sig");
  const ok = await dbc.verifySignature(new Uint8Array(fs.readFileSync(so)), new Uint8Array(fs.readFileSync(sig)));
  assert(ok === true, "verifySignature failed for a valid signature");

  await dbc.uninstall("test-driver-1", installDir);
  assert((await dbc.listInstalled(installDir)).length === 0, "driver still listed after uninstall");

  await dbc.close();
  fs.rmSync(installDir, { recursive: true, force: true });
  server.close();
  console.log("WORKER SMOKE PASS:", { drivers: search.drivers.length, installed: manifest.id, verified: ok });
}

main().catch((e) => {
  console.error("WORKER SMOKE FAIL:", e && e.stack ? e.stack : e);
  process.exit(1);
});
