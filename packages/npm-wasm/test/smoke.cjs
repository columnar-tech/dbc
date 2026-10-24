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
const crypto = require("crypto");
const fs = require("fs");
const http = require("http");
const os = require("os");
const path = require("path");

const { loadDbc } = require("..");

const REPO_ROOT = path.resolve(__dirname, "..", "..", "..");
const indexData = fs.readFileSync(path.join(REPO_ROOT, "cmd/dbc/testdata/test_index.yaml"));
const tarData = fs.readFileSync(path.join(REPO_ROOT, "cmd/dbc/testdata/test-driver-1.tar.gz"));
const manifestOnlyTarData = fs.readFileSync(path.join(REPO_ROOT, "cmd/dbc/testdata/test-driver-manifest-only.tar.gz"));

function listEntries(root, directory = root, entries = []) {
  for (const name of fs.readdirSync(directory)) {
    const entryPath = path.join(directory, name);
    entries.push(path.relative(root, entryPath));
    if (fs.lstatSync(entryPath).isDirectory()) {
      listEntries(root, entryPath, entries);
    }
  }
  return entries.sort();
}

const server = http.createServer((req, res) => {
  if (req.url.startsWith("/index.yaml")) {
    res.setHeader("Content-Type", "application/yaml");
    res.end(indexData);
  } else if (req.url.includes("test-driver-manifest-only")) {
    res.setHeader("Content-Type", "application/gzip");
    res.setHeader("Content-Length", String(manifestOnlyTarData.length));
    res.end(manifestOnlyTarData);
  } else if (req.url.includes(".tar.gz")) {
    res.setHeader("Content-Type", "application/gzip");
    res.setHeader("Content-Length", String(tarData.length));
    res.end(tarData);
  } else {
    res.statusCode = 404;
    res.end("not found");
  }
});

async function main() {
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const base = `http://127.0.0.1:${server.address().port}`;

  const dbc = await loadDbc({ baseURL: base, platform: "linux_amd64" });

  const search = await dbc.search("");
  assert(Array.isArray(search.drivers) && search.drivers.length > 0, "search returned no drivers");

  const resolved = await dbc.resolve("test-driver-1", "linux_amd64");
  assert(resolved.versions.length > 0, "resolve returned no versions");

  const installDir = fs.mkdtempSync(path.join(os.tmpdir(), "dbc-wasm-smoke-"));
  const externalLibrary = path.join(installDir, "external-driver.so");
  const externalBytes = Buffer.from("externally managed driver library");
  fs.writeFileSync(externalLibrary, externalBytes);
  const manifest = await dbc.install("test-driver-1", installDir);
  assert(manifest.driverPath && fs.existsSync(manifest.driverPath), "installed driver missing on disk");
  const generationDir = path.dirname(manifest.driverPath);
  assert(fs.existsSync(generationDir), "installed package generation missing on disk");
  const installedBytes = fs.readFileSync(manifest.driverPath);
  const receiptPath = path.join(generationDir, "dbc-install-receipt.json");
  const receiptBytes = fs.readFileSync(receiptPath);
  const receipt = JSON.parse(receiptBytes);
  assert.strictEqual(receipt.generation, path.basename(generationDir), "receipt does not identify the installed generation");
  assert.strictEqual(
    receipt.owned_library_sha256,
    crypto.createHash("sha256").update(installedBytes).digest("hex"),
    "receipt hash does not match the installed library"
  );

  const entriesBeforeManifestOnlyInstall = listEntries(installDir);
  await assert.rejects(
    dbc.install("test-driver-manifest-only", installDir),
    /does not specify Files\.driver/,
    "manifest-only package should be rejected without Files.driver"
  );
  assert.deepStrictEqual(
    listEntries(installDir),
    entriesBeforeManifestOnlyInstall,
    "rejected manifest-only package published files"
  );
  assert(!fs.existsSync(path.join(installDir, "test-driver-manifest-only.toml")), "manifest-only registration was published");
  assert.deepStrictEqual(fs.readFileSync(manifest.driverPath), installedBytes, "rejected package changed the installed driver bytes");
  assert.deepStrictEqual(fs.readFileSync(receiptPath), receiptBytes, "rejected package changed the installed receipt");

  // Keep a separate registration backed by an external library so uninstall
  // continues to cover the existing rule that external files are not owned.
  fs.writeFileSync(
    path.join(installDir, "external-borrower.toml"),
    `name = "External Borrower"\nversion = "1.0.0"\nsource = "external"\n\n[Driver.shared]\nlinux_amd64 = ${JSON.stringify(externalLibrary)}\n`
  );

  const installed = await dbc.listInstalled(installDir);
  assert(
    installed.length === 2 && installed.some((driver) => driver.id === "test-driver-1") && installed.some((driver) => driver.id === "external-borrower"),
    `listInstalled mismatch: got ${JSON.stringify(installed)}; installDir entries: ${JSON.stringify(fs.readdirSync(installDir, { recursive: true }))}`
  );

  const so = manifest.driverPath;
  const sig = `${manifest.driverPath}.sig`;
  const ok = await dbc.verifySignature(new Uint8Array(fs.readFileSync(so)), new Uint8Array(fs.readFileSync(sig)));
  assert(ok === true, "verifySignature failed for a valid signature");

  await dbc.uninstall("test-driver-1", installDir);
  const after = await dbc.listInstalled(installDir);
  assert(
    after.length === 1 && after[0].id === "external-borrower",
    `registration state after uninstall mismatch: ${JSON.stringify(after)}`
  );
  assert(!fs.existsSync(path.join(installDir, "test-driver-1.toml")), "package registration remains after uninstall");
  assert(fs.existsSync(generationDir), "un-pinned package generation was not retained after uninstall");
  assert.deepStrictEqual(fs.readFileSync(manifest.driverPath), installedBytes, "retained package library bytes changed after uninstall");
  assert.deepStrictEqual(fs.readFileSync(receiptPath), receiptBytes, "retained package receipt changed after uninstall");
  assert.deepStrictEqual(fs.readFileSync(externalLibrary), externalBytes, "external library changed after uninstall");
  assert(fs.existsSync(path.join(installDir, "external-borrower.toml")), "external library registration was removed");

  await dbc.uninstall("external-borrower", installDir);
  assert(!(await dbc.listInstalled(installDir)).some((driver) => driver.id === "external-borrower"), "external registration remains after uninstall");
  assert(!fs.existsSync(path.join(installDir, "external-borrower.toml")), "external registration manifest remains after uninstall");
  assert.deepStrictEqual(fs.readFileSync(externalLibrary), externalBytes, "external library changed when its registration was removed");

  // Regression guard (roborev 6562): in-process loadDbc() must namespace
  // load-time client-construction failures with `dbc-wasm:`, matching the worker
  // backend. An invalid credential registryURL (NUL byte) makes the underlying
  // dbcNewClient reject; the error must surface through prefixError().
  let initErrorPrefixed = false;
  try {
    await loadDbc({
      baseURL: base,
      platform: "linux_amd64",
      credential: { registryURL: "http://\u0000", authURI: "http://example.test", token: "t" },
    });
  } catch (e) {
    initErrorPrefixed = String(e && e.message ? e.message : e).startsWith("dbc-wasm:");
  }
  assert(initErrorPrefixed, "in-process loadDbc() init failure should reject with a dbc-wasm:-prefixed error");

  fs.rmSync(installDir, { recursive: true, force: true });
  server.close();
  console.log("SMOKE PASS:", {
    drivers: search.drivers.length,
    resolvedVersions: resolved.versions,
    installed: manifest.id,
    verified: ok,
  });
}

main().catch((e) => {
  console.error("SMOKE FAIL:", e && e.stack ? e.stack : e);
  process.exit(1);
});
