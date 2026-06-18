// Phase 0 spike harness for the dbc WASM library.
// Loads the wasm module under Node, serves the repo test fixtures over a local
// HTTP server (so the fetch RoundTripper is exercised for real), and runs the
// core acceptance criteria: R1 paths, search, install -> list -> verify ->
// uninstall, plus a concurrency check for event-loop deadlocks.
const fs = require("fs");
const http = require("http");
const path = require("path");
const os = require("os");

// Under Node js/wasm the loader must supply the real fs/process before
// wasm_exec.js, otherwise Go's syscalls hit stubs ("not implemented on js").
globalThis.fs = fs;
globalThis.process = process;

require(process.env.WASM_EXEC); // defines global.Go

const WORKTREE = process.env.WORKTREE;
const WASM = process.env.WASM;
const indexData = fs.readFileSync(path.join(WORKTREE, "cmd/dbc/testdata/test_index.yaml"));
const tarData = fs.readFileSync(path.join(WORKTREE, "cmd/dbc/testdata/test-driver-1.tar.gz"));

let tarballRequests = 0;
const server = http.createServer((req, res) => {
  if (req.url.startsWith("/index.yaml")) {
    res.setHeader("Content-Type", "application/yaml");
    res.end(indexData);
  } else if (req.url.includes(".tar.gz")) {
    tarballRequests++;
    res.setHeader("Content-Type", "application/gzip");
    res.setHeader("Content-Length", String(tarData.length));
    res.end(tarData);
  } else {
    res.statusCode = 404;
    res.end("not found");
  }
});

function findFile(dir, predicate) {
  for (const entry of fs.readdirSync(dir, { recursive: true })) {
    const full = path.join(dir, entry);
    if (fs.statSync(full).isFile() && predicate(entry.toString())) return full;
  }
  return null;
}

async function main() {
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const base = `http://127.0.0.1:${server.address().port}`;

  const go = new Go();
  go.env = process.env; // R1: Go's js/wasm env is empty by default; propagate HOME/XDG
  const { instance } = await WebAssembly.instantiate(fs.readFileSync(WASM), go.importObject);
  go.run(instance); // runs main(), registers globals, parks on select{}
  await new Promise((r) => setTimeout(r, 0));

  if (typeof globalThis.dbcSearch !== "function") throw new Error("globals not registered");

  globalThis.dbcSetBaseURL(base);
  globalThis.dbcSetPlatform("linux_amd64");

  const out = {};

  // R1: do os.UserConfigDir / UserHomeDir resolve under Node js/wasm?
  out.debugPaths = JSON.parse(await globalThis.dbcDebugPaths());

  // search over HTTP via the fetch RoundTripper
  out.search = JSON.parse(await globalThis.dbcSearch(""));

  // resolve versions + latest package URL for a driver/platform
  out.resolve = JSON.parse(await globalThis.dbcResolve("test-driver-1", "linux_amd64"));

  // install to disk (download -> temp -> extract -> manifest + symlink)
  const installDir = fs.mkdtempSync(path.join(os.tmpdir(), "dbc-install-"));
  out.install = JSON.parse(await globalThis.dbcInstall("test-driver-1", installDir));

  // list installed
  out.list = JSON.parse(await globalThis.dbcList(installDir));

  // verify signature using the extracted .so + .so.sig
  const soFile = findFile(installDir, (f) => f.endsWith(".so"));
  const sigFile = findFile(installDir, (f) => f.endsWith(".sig"));
  out.found = { soFile, sigFile };
  if (soFile && sigFile) {
    const lib = new Uint8Array(fs.readFileSync(soFile));
    const sig = new Uint8Array(fs.readFileSync(sigFile));
    try {
      out.verifyValid = await globalThis.dbcVerify(lib, sig);
    } catch (e) {
      out.verifyValid = "ERROR: " + e.message;
    }
    // negative case: corrupted signature should fail
    const badSig = new Uint8Array([1, 2, 3, 4]);
    try {
      await globalThis.dbcVerify(lib, badSig);
      out.verifyInvalid = "UNEXPECTED_SUCCESS";
    } catch (e) {
      out.verifyInvalid = "rejected_as_expected";
    }
  }

  // installed files snapshot
  out.installedFiles = fs.readdirSync(installDir, { recursive: true }).map(String).sort();

  // uninstall, then list again
  out.uninstall = await globalThis.dbcUninstall("test-driver-1", installDir);
  out.listAfter = JSON.parse(await globalThis.dbcList(installDir));

  // concurrency / deadlock check: fire several searches at once
  const concurrent = await Promise.all(
    Array.from({ length: 5 }, () => globalThis.dbcSearch("test-driver-1").then((s) => JSON.parse(s).drivers.length))
  );
  out.concurrentSearchCounts = concurrent;
  out.tarballRequests = tarballRequests;

  console.log(JSON.stringify(out, null, 2));
  server.close();
  process.exit(0);
}

main().catch((e) => {
  console.error("HARNESS ERROR:", e && e.stack ? e.stack : e);
  process.exit(1);
});
