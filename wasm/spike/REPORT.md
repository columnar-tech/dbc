# Phase 0 De-Risk Spike — Report

**Result: PASS.** All Phase 0 acceptance criteria are green on Linux (Node v24.11.1, Go 1.26.4 at spike time; CI now pins Go 1.25 — see `.github/workflows/wasm.yml`). The architecture in `.sisyphus/plans/wasm-js-interface.md` is confirmed; proceed to Phase 1/2.

## Acceptance criteria

| Criterion | Result |
|---|---|
| `GOOS=js GOARCH=wasm -tags dbcnode` build succeeds | PASS (15.4 MB uncompressed) |
| R1: `os.UserConfigDir`/`os.UserHomeDir` resolve; `config` reads work | PASS — **with loader setup (see below)** |
| Platform tuple override (`unknown_wasm64` → host) | PASS (`linux_amd64`) |
| `search` over HTTP via injected fetch transport | PASS (7 drivers; no fake-network) |
| Private-registry injected creds + 401 → refresh → retry | PASS (full chain below) |
| Auth-internal `http.DefaultClient` flows through fetch (Momus risk R7) | PASS (OIDC GET + token POST observed via fetch) |
| `install` → download → temp → extract → manifest + symlink | PASS |
| `list` installed; `uninstall`; cleanup | PASS (`listAfter=[]`) |
| `verifySignature` valid→true, invalid→rejected (gopenpgp+circl at runtime) | PASS |
| No event-loop deadlock under concurrent calls | PASS (5 parallel searches) |
| Native build + tests (`./ ./config ./auth`) unaffected | PASS |

## R1 outcome (the key finding) — loader requirements

Node js/wasm works, but the bare `wasm_exec.js` is browser-oriented. The JS loader (Phase 3 npm package) MUST, before instantiating:

1. `globalThis.fs = require("fs")` and `globalThis.process = require("process")` **before** loading `wasm_exec.js` — otherwise Go's filesystem syscalls return `not implemented on js`. (This is what upstream `wasm_exec_node.js` does.)
2. `go.env = process.env` — Go's js/wasm environment is empty by default, so `$HOME`/`$XDG_*` are missing and `os.UserConfigDir`/`os.UserHomeDir` fail (`$HOME is not defined`).

With both in place: `userConfigDir=/home/.../.config`, `userHomeDir=/home/...`, install/list/uninstall against the real filesystem all work. The explicit-`Location` fallback was therefore NOT needed on Linux/macOS.

## Auth chain (Momus risk R7 resolved)

Injected an OAuth credential with a stale token; mock private registry returned 401. Observed server sequence:

```
GET  /index.yaml              Bearer stale-token   -> 401
GET  /.well-known/openid-configuration  (none)     -> OIDC discovery via fetch
POST /token  (refresh_token=refresh-1)  (none)     -> refresh via fetch (POST body OK)
GET  /index.yaml              Bearer good-token    -> 200
```

This confirms the `http.DefaultClient`/`dbc.DefaultClient` override in the entrypoint `init()` routes dbc's auth-internal calls (`auth/oauth.go`, `auth/credentials.go`) through the fetch transport, and that POST-with-body works through the RoundTripper.

## Change set

> **Note (superseded):** this records the Phase 0 spike layout. The shipped
> implementation split the single `wasm/main_node.go` API surface across
> `wasm/api_js.go`, `wasm/ops_node_js.go`, `wasm/bridge_js.go`, and
> `wasm/roundtripper_js.go`; `wasm/main_node.go` is now a thin entrypoint. Use
> the current tree (not this list) to navigate the code.

- `client.go` — telemetry call swap; removed direct `machine-id` import; added exported `WithCredentialResolver`.
- `drivers.go` — telemetry call swap; removed direct `machine-id` import.
- `telemetry_other.go` (`//go:build !js`), `telemetry_wasm.go` (`//go:build js`) — `telemetryMachineID()` seam.
- `config/platform_override.go` (`//go:build js`) — `SetPlatformTupleOverride`.
- `wasm/bridge_js.go`, `wasm/roundtripper_js.go` (`//go:build js`) — Promise bridge + fetch RoundTripper.
- `wasm/main_node.go` (`//go:build js && dbcnode`) — API: search/install/list/uninstall/verify + platform/baseURL/credential hooks; `http.DefaultClient` override.
- `wasm/spike/` — Node harnesses (this report's evidence).

## Deviations from the plan

- Entrypoint lives at repo-root `wasm/`, not `cmd/dbc-wasm/` (it's a library artifact, not a CLI command). Still `package main` (required by the `syscall/js` model).
- `SetPlatformTupleOverride` is gated `//go:build js` (the override is meaningless on native, where detection is correct).

## How to reproduce

```sh
bash wasm/spike/run.sh
```

## Next (Phase 1/2)

> **Status update:** Phases 1–2 were folded into the Phase 3 npm-package work and
> Phase 4 (worker backend, Windows-host groundwork) rather than shipped as
> separately labeled phases. The `main_browser.go` browser seam was added but is
> **not yet delivered** (no loader, npm entry, or smoke test); it remains a
> tracked future phase. The single canonical type declaration is
> `packages/npm-wasm/index.d.ts`.

- Productionize: unit-test `WithCredentialResolver`; add `main_browser.go` (`//go:build js && !dbcnode`) for the browser seam; JSON DTOs + `.d.ts`.
- Add `resolve` (versions/URLs) to the API.
- Phase 3 npm loader must encode the two R1 loader requirements above.
