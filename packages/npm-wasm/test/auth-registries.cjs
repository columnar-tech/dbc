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

// Both entrypoints call this before loading any Go runtime, including workers.
// Isolate all paths curated by boot.cjs, not just the credential file.
function isolateEnvironment(label) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), `dbc-wasm-${label}-`));
  for (const [key, dir] of Object.entries({
    HOME: "home",
    USERPROFILE: "home",
    AppData: "appdata",
    LocalAppData: "localappdata",
    XDG_CONFIG_HOME: "config",
    XDG_DATA_HOME: "data",
    XDG_CACHE_HOME: "cache",
    TMPDIR: "tmp",
  })) {
    const location = path.join(root, dir);
    fs.mkdirSync(location, { recursive: true });
    process.env[key] = location;
  }
  process.once("exit", () => fs.rmSync(root, { recursive: true, force: true }));
  return root;
}

async function testAuthRegistries(loadDbc, root, worker) {
  const authorizations = [];
  const server = http.createServer((req, res) => {
    authorizations.push(req.headers.authorization);
    let name;
    if (req.url.startsWith("/one/index.yaml")) {
      name = "one";
    } else if (req.url.startsWith("/two/index.yaml")) {
      name = "two";
    } else {
      res.writeHead(404).end("not found");
      return;
    }
    res.setHeader("Content-Type", "application/yaml");
    res.end(`drivers:\n  - name: Fixture ${name}\n    path: fixture-${name}\n    description: Local registry fixture\n    license: MIT\n`);
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const base = `http://127.0.0.1:${server.address().port}`;
  const one = `${base}/one`;
  const two = `${base}/two`;
  const configDir = process.platform === "win32"
    ? path.join(root, "appdata", "Columnar", "dbc")
    : process.platform === "darwin"
      ? path.join(root, "home", "Library", "Application Support", "Columnar", "dbc")
      : path.join(root, "config", "columnar", "dbc");
  const globalFile = path.join(configDir, "config.toml");
  const projectFile = path.join(root, "dbc.toml");
  const credentialFile = path.join(root, "data", "dbc", "credentials", "credentials.toml");
  const missingProject = path.join(root, "missing.toml");
  fs.mkdirSync(configDir, { recursive: true });
  fs.writeFileSync(globalFile, `replace_defaults = true\n[[registries]]\nurl = "${one}"\nname = "global"\n`);
  fs.writeFileSync(projectFile, `replace_defaults = true\n[[registries]]\nurl = "${two}"\nname = "project"\n`);

  const open = (opts = {}) => loadDbc({ worker, platform: "linux_amd64", ...opts });
  const search = async (opts, expected) => {
    const client = await open({ credentialStore: "none", ...opts });
    try {
      const result = await client.search();
      assert.deepStrictEqual(result.drivers.map((driver) => driver.path), expected);
      assert.strictEqual(result.warning, undefined, "registry selection should not contact defaults");
    } finally {
      await client.close();
    }
  };
  const rejectsConfig = (opts, pattern) => assert.rejects(async () => {
    const client = await open(opts);
    await client.close();
  }, { message: pattern });
  const assertAuthorization = async (opts, expected) => {
    const client = await open({ baseURL: one, ...opts });
    try {
      authorizations.length = 0;
      await client.search();
      assert.strictEqual(authorizations.length, 1);
      assert.strictEqual(authorizations[0], expected);
    } finally {
      await client.close();
    }
  };

  try {
    // Omitting registryConfig intentionally honors the user's global file.
    await search({}, ["fixture-one"]);
    await search({ registryConfig: undefined }, ["fixture-one"]);
    await search({ registryConfig: "default" }, ["fixture-one"]);
    // An installation target override must never select a different host store.
    await search({ platform: process.platform === "win32" ? "macos_arm64" : "windows_amd64" }, ["fixture-one"]);
    await search({ projectFile }, ["fixture-two", "fixture-one"]);
    await search({ registryConfig: "none", projectFile }, ["fixture-two"]);

    fs.writeFileSync(globalFile, "this is not valid TOML [");
    await rejectsConfig({ credentialStore: "none" }, /^dbc-wasm:/);
    const noConfig = await open({ registryConfig: "none", credentialStore: "none" });
    await noConfig.close();
    await rejectsConfig({ registryConfig: "none", projectFile: missingProject }, /^dbc-wasm:/);
    await rejectsConfig({ registryConfig: "invalid" }, /^dbc-wasm:.*registryConfig/);

    // Explicit endpoints bypass even invalid config selectors and missing files.
    await search({
      registries: [{ url: two, name: "second" }, { url: one, name: "first" }],
      registryConfig: "invalid",
      projectFile: missingProject,
    }, ["fixture-two", "fixture-one"]);
    await search({ registries: [], registryConfig: "default", projectFile: missingProject }, []);
    await search({ baseURL: one, registryConfig: "default", projectFile: missingProject }, ["fixture-one"]);
    await rejectsConfig({ baseURL: one, registries: [{ url: two }] }, /^dbc-wasm:.*baseURL/);
    await rejectsConfig({ baseURL: one, registries: [] }, /^dbc-wasm:.*baseURL/);
    await rejectsConfig({ registries: [{ url: "http://\u0000" }] }, /^dbc-wasm:/);
    await rejectsConfig({ baseURL: one, credentialStore: "invalid" }, /^dbc-wasm:.*credentialStore/);
    for (const value of [false, 0]) {
      await rejectsConfig({ baseURL: one, registryConfig: value }, /^dbc-wasm:.*invalid client config.*registryConfig/);
      await rejectsConfig({ baseURL: one, credentialStore: value }, /^dbc-wasm:.*invalid client config.*credentialStore/);
    }

    // A disabled store must not read even a malformed credential file. This is
    // before the first store-backed request, so the in-process cache cannot hide it.
    fs.mkdirSync(path.dirname(credentialFile), { recursive: true });
    fs.writeFileSync(credentialFile, "invalid credentials [");
    await assertAuthorization({ credentialStore: "none" }, undefined);
    fs.unlinkSync(credentialFile);

    // Disabling authentication fallback must not disable explicit store operations.
    const client = await open({ baseURL: one, credentialStore: "none" });
    const credential = { registryURL: one, authURI: `${base}/oauth`, token: "stored-first" };
    try {
      assert.strictEqual(await client.login(credential), undefined);
      assert(fs.readFileSync(credentialFile, "utf8").includes("stored-first"), "login must persist the token");
      await assertAuthorization({}, "Bearer stored-first");
      await assertAuthorization({ credentialStore: undefined }, "Bearer stored-first");
      await client.login({ ...credential, token: "stored-replacement" });
      await assertAuthorization({}, "Bearer stored-replacement");
      await assert.rejects(client.login({ ...credential, registryURL: "http://\u0000" }), { message: /^dbc-wasm:/ });
      await assert.rejects(client.login({ ...credential, authURI: "http://\u0000" }), { message: /^dbc-wasm:/ });
      await assert.rejects(client.logout("http://\u0000"), { message: /^dbc-wasm:/ });

      const otherHost = { registryURL: "https://other.example.test", authURI: "https://other.example.test", token: "injected" };
      await assertAuthorization({ credential: otherHost }, "Bearer stored-replacement");
      await assertAuthorization({ credentialStore: "default", credential: { ...credential, token: "injected" } }, "Bearer injected");
      await assertAuthorization({ credentialStore: "none" }, undefined);
      await assertAuthorization({ credentialStore: "none", credential: otherHost }, undefined);
      await assertAuthorization({ credentialStore: "none", credential: { ...credential, token: "injected" } }, "Bearer injected");

      const licensePath = await client.licensePath();
      assert.strictEqual(path.resolve(licensePath), path.join(path.dirname(credentialFile), "columnar.lic"));
      assert(!fs.existsSync(licensePath), "licensePath must not install a license");
      const source = path.join(root, "columnar.lic");
      const renamedSource = path.join(root, "renamed.lic");
      fs.writeFileSync(source, "first license\n");
      fs.writeFileSync(renamedSource, "replacement license\n");
      const forceError = (err) => /^dbc-wasm:/.test(err.message) && /force/.test(err.message) && !/--force/.test(err.message);
      await assert.rejects(client.installLicense(renamedSource), forceError);
      assert.strictEqual(await client.installLicense(source), undefined);
      assert.strictEqual(fs.readFileSync(licensePath, "utf8"), "first license\n");
      await assert.rejects(client.installLicense(source), forceError);
      assert.strictEqual(fs.readFileSync(licensePath, "utf8"), "first license\n");
      assert.strictEqual(await client.installLicense(renamedSource, { force: true }), undefined);
      assert.strictEqual(fs.readFileSync(licensePath, "utf8"), "replacement license\n");
      await assert.rejects(client.installLicense(path.join(root, "absent", "columnar.lic"), { force: true }), { message: /^dbc-wasm:/ });

      assert.strictEqual(await client.logout(one), undefined);
      assert(!fs.readFileSync(credentialFile, "utf8").includes("stored-replacement"), "logout must remove the stored token");
      await assertAuthorization({}, undefined);
      await assert.rejects(client.logout(one), { message: /^dbc-wasm:.*no credentials found/ });
    } finally {
      await client.close();
    }
    await assert.rejects(client.login(credential), { message: /^dbc-wasm: client is closed/ });
    await assert.rejects(client.logout(one), { message: /^dbc-wasm: client is closed/ });
    await assert.rejects(client.licensePath(), { message: /^dbc-wasm: client is closed/ });
    await assert.rejects(client.installLicense(projectFile), { message: /^dbc-wasm: client is closed/ });

    // Long-lived runtimes may cache lookups, but mutations must merge the latest
    // disk contents so independent clients never lose or resurrect other hosts.
    const first = await open({ baseURL: one, credentialStore: "none" });
    let second;
    const other = { registryURL: "https://other.example.test", authURI: "https://other.example.test", token: "other-host-token" };
    try {
      second = await open({ baseURL: one, credentialStore: "none" });
      await first.login({ ...credential, token: "first-host-token" });
      await second.login(other);
      await first.login({ ...credential, token: "updated-first-host-token" });
      let stored = fs.readFileSync(credentialFile, "utf8");
      assert(stored.includes("other-host-token"), "updating one host must preserve another runtime's login");
      assert(stored.includes("updated-first-host-token"), "updating one host must persist its new token");
      await first.logout(one);
      stored = fs.readFileSync(credentialFile, "utf8");
      assert(stored.includes("other-host-token"), "logout must preserve another runtime's login");
      assert(!stored.includes("updated-first-host-token"), "logout must remove the updated token");
      await second.logout(other.registryURL);
      stored = fs.readFileSync(credentialFile, "utf8");
      assert(!stored.includes("other-host-token"), "second logout must remove its own token");
      assert(!stored.includes("updated-first-host-token"), "stale runtimes must not resurrect removed hosts");
    } finally {
      await first.close();
      if (second) await second.close();
    }
  } finally {
    fs.rmSync(globalFile, { force: true });
    await new Promise((resolve) => server.close(resolve));
  }
}

module.exports = { isolateEnvironment, testAuthRegistries };
