// Phase 0 auth validation: proves a private-registry 401 -> OAuth refresh ->
// retry cycle works end-to-end, and that dbc's auth-internal http.DefaultClient
// calls (OIDC discovery GET + token POST) flow through the fetch transport.
const fs = require("fs");
const http = require("http");

globalThis.fs = fs;
globalThis.process = process;
require(process.env.WASM_EXEC);

const WORKTREE = process.env.WORKTREE;
const WASM = process.env.WASM;
const indexData = fs.readFileSync(WORKTREE + "/cmd/dbc/testdata/test_index.yaml");

let base = "";
const seen = [];
const server = http.createServer((req, res) => {
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    const auth = req.headers["authorization"] || "";
    const p = req.url.split("?")[0];
    seen.push(`${req.method} ${p} auth=${auth || "(none)"}`);
    if (p === "/index.yaml") {
      if (auth === "Bearer good-token") {
        res.setHeader("Content-Type", "application/yaml");
        res.end(indexData);
      } else {
        res.statusCode = 401;
        res.end("unauthorized");
      }
    } else if (p === "/.well-known/openid-configuration") {
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify({ issuer: base, token_endpoint: base + "/token" }));
    } else if (p === "/token" && req.method === "POST") {
      const ok = body.includes("grant_type=refresh_token") && body.includes("refresh_token=refresh-1");
      res.statusCode = ok ? 200 : 400;
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify(ok ? { access_token: "good-token" } : { error: "bad_refresh" }));
    } else {
      res.statusCode = 404;
      res.end("not found");
    }
  });
});

async function main() {
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  base = `http://127.0.0.1:${server.address().port}`;

  const go = new Go();
  go.env = process.env;
  const { instance } = await WebAssembly.instantiate(fs.readFileSync(WASM), go.importObject);
  go.run(instance);
  await new Promise((r) => setTimeout(r, 0));

  globalThis.dbcSetPlatform("linux_amd64");
  const cfg = JSON.stringify({
    baseURL: base,
    credential: { registryURL: base, authURI: base, token: "stale-token", refreshToken: "refresh-1", clientID: "client-1" },
  });

  const out = {};
  try {
    const drivers = JSON.parse(await globalThis.dbcSearch(cfg, ""));
    out.searchOK = drivers.drivers.length;
  } catch (e) {
    out.searchError = e.message;
  }
  out.serverLog = seen;
  console.log(JSON.stringify(out, null, 2));
  server.close();
  process.exit(out.searchOK ? 0 : 1);
}

main().catch((e) => {
  console.error("AUTH HARNESS ERROR:", e && e.stack ? e.stack : e);
  process.exit(1);
});
