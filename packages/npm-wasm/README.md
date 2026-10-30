<!--
Copyright 2026 Columnar Technologies Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# @columnar-tech/dbc-wasm

A WebAssembly build of [dbc](https://github.com/columnar-tech/dbc) that runs
in **Node.js** as an importable library — search registries, resolve versions,
install/uninstall ADBC drivers to disk, manage stored tokens and licenses, and
verify signatures, without spawning a subprocess.

> For the command-line tool, use [`@columnar-tech/dbc`](https://www.npmjs.com/package/@columnar-tech/dbc) instead.

## Requirements

- Node.js >= 18 (uses global `fetch`; the loader wires Node's `fs`/`process`/`webcrypto` into the wasm runtime automatically).

## Runtime support

Validated on **Node.js >= 18** and **Deno** (`deno run -A`). **Bun is not currently
supported**: Bun's WebAssembly ↔ `node:fs` bridge mishandles the values Go's
`GOOS=js` runtime passes to fs callbacks, so file-writing operations
(`install`/`uninstall`) fail with `ERR_OUT_OF_RANGE`. This is an upstream Bun bug
([oven-sh/bun#32505](https://github.com/oven-sh/bun/issues/32505)), not a
limitation of this package; read-only `search`/`resolve` may still work.

The browser WASM target is experimental and has no published npm entrypoint or
loader. It exposes search/resolve/signature verification, not filesystem
operations. Its implicit `registryConfig` is always `"none"`; explicit
`registryConfig: "default"` and `projectFile` reject as Node-only, unless an
explicit `baseURL` or `registries` overrides those options entirely.

## Platform support

Linux and macOS are validated in CI. **Windows host support is experimental and
not yet exercised in CI** — `windows-latest` is excluded from the WASM workflow
while driver discovery under `GOOS=js` is finished (tracked in
[#396](https://github.com/columnar-tech/dbc/issues/396)). The loader normalizes
Windows paths (backslashes to forward slashes; drive-relative to absolute) and
the Go config layer no longer splits a drive-lettered `location` on `:`, but the
end-to-end install/list round-trip is not yet validated on a Windows runtime.
Known Windows caveats:

- Pass an explicit `location` (the npm API already does). Registry-backed
  user/system config levels are unavailable under WASM.
- The manifest-symlink compatibility shim (for the ADBC Python driver-manager
  <= 1.8.0) is inactive without Developer Mode; use driver-manager >= 1.8.1.

## Usage

```js
import { loadDbc } from "@columnar-tech/dbc-wasm";

const dbc = await loadDbc();

// Search the configured registries
const { drivers, warning } = await dbc.search("snowflake");
if (warning) console.warn("some registries were unavailable:", warning);

// Resolve versions + the latest package URL for the host platform
const info = await dbc.resolve("snowflake");

// Install to a directory (ADBC_DRIVER_PATH-style location)
const manifest = await dbc.install("snowflake", "/etc/adbc/drivers");

// List / uninstall
const installed = await dbc.listInstalled("/etc/adbc/drivers");
await dbc.uninstall("snowflake", "/etc/adbc/drivers");
```

On WebAssembly, uninstall removes the driver registration while retaining the installed package files.

CommonJS works too:

```js
const { loadDbc } = require("@columnar-tech/dbc-wasm");
```

### Options

```js
await loadDbc({
  registries: [                               // used verbatim; do not also set baseURL
    { url: "https://my-registry.example.com", name: "private" },
  ],
  platform: "linux_amd64",                     // defaults to the detected Node host
  credentialStore: "default",                 // use stored credentials for other hosts
  credential: {                              // injected private-registry auth
    registryURL: "https://my-registry.example.com",
    authURI: "https://my-registry.example.com",
    token: "...",
    refreshToken: "...",
    clientID: "...",
  },
  worker: true,                               // run the wasm in a Node Worker Thread
});
```

Registry configuration and injected credentials are **per-instance**.
`platform`, however, is applied **process-globally** by the in-process backend
(the last `loadDbc({ platform })` wins), so concurrent in-process clients can't
each pin a different default platform. For per-call platform behavior, pass
`platform` to `resolve(name, platform)`, which takes precedence over the
load-time default.

Pass `worker: true` to run the runtime in a Node `worker_threads` Worker, so large
installs and signature verification stay off your app's event loop. **Always
`await dbc.close()` when done** under `worker: true` — the worker thread is not
garbage-collected, so a missing `close()` leaks the thread and keeps the Node
process alive. (In the default in-process backend `close()` is optional; the
handle is also released on GC.)

### Registry configuration

**By default, Node clients now honor the user's global dbc `config.toml`.**
Omitting `registryConfig` (or setting `"default"`) loads it; a missing global
file is allowed, but malformed or unreadable files reject `loadDbc()`. This is
an intentional change from always using only the built-in registries.

```js
const dbc = await loadDbc({
  registryConfig: "default",
  projectFile: "/workspace/dbc.toml", // optional, Node-only; missing file is an error
});
```

The project file contributes only `registries` and `replace_defaults`, using
the CLI's project → global → built-in registry precedence. Set
`registryConfig: "none"` to skip the global file; it does not disable an explicit
`projectFile`.

Alternatively, set `baseURL` for one registry, or `registries` for an ordered list
of `{ url, name? }` entries. These two options are mutually exclusive. An explicit
list is used verbatim, including `[]` to disable all registries. Either explicit
option ignores `registryConfig` and `projectFile`, without reading their files.
Unrecognized `registryConfig` strings are ignored in this case; malformed JSON
field types are still rejected.

### Stored credentials and licenses (Node-only)

Omitting `credentialStore` or setting `"default"` uses dbc's stored credentials.
An injected `credential` replaces the stored credential for its registry host;
requests to other hosts still fall back to the store.
`credentialStore: "none"`: stored credentials are never used to authenticate;
refreshing an injected credential can still update dbc's credential/license
files. Injected credentials still work with `"none"`, but other hosts have no
stored-credential fallback.

Other already-running runtimes may retain cached credential lookups after the
store changes. Recreate a worker to refresh its cache; the in-process backend
requires restarting the process, not just creating another `loadDbc()` client.

The following methods are available in both direct and worker mode:

```js
const dbc = await loadDbc({ registryConfig: "none" });
try {
  // Save an existing token; this does not start an OAuth/device login flow.
  // Any credential for this registry host is overwritten.
  await dbc.login({
    registryURL: "https://my-registry.example.com",
    authURI: "https://my-registry.example.com",
    token: "...",
    refreshToken: "...", // optional
    clientID: "...",     // optional
  });

  const destination = await dbc.licensePath(); // no file is created
  await dbc.installLicense("/downloads/columnar.lic");
  // force allows a different source filename and/or overwriting the destination.
  await dbc.installLicense("/downloads/renewed.lic", { force: true });
  await dbc.logout("https://my-registry.example.com");
} finally {
  await dbc.close();
}
```

`login`/`logout` and license operations act on the shared user store, regardless
of the client's `credentialStore` setting. `logout` rejects if no credential
exists for that host. `installLicense` copies bytes without validating license
contents; without `force`, the source must be named `columnar.lic` and an existing
license is not overwritten. All failures reject with the `dbc-wasm:` prefix.

Node uses the same default config and credential/license locations as the native
CLI, based on the actual host OS, not the `platform` installation-target option:

| Host | Registry config directory | Credential/license directory |
| --- | --- | --- |
| Linux/Unix | `$XDG_CONFIG_HOME/columnar/dbc`, or `$HOME/.config/columnar/dbc` | `$HOME/.local/share/dbc/credentials` |
| macOS | `$HOME/Library/Application Support/Columnar/dbc` | The config directory's `credentials` subdirectory |
| Windows | `%AppData%\Columnar\dbc` | `%LocalAppData%\dbc\credentials` |

On every host, a nonempty `XDG_DATA_HOME` overrides the credential/license base
with `XDG_DATA_HOME/dbc/credentials` and must be absolute. macOS and Windows ignore
`XDG_CONFIG_HOME`, matching the native CLI. Windows paths are normalized only at
the WASM boundary.

The runtime captures its bounded path environment when it starts. Set these
variables before the first `loadDbc()` (or worker creation) to select a particular
store or isolate tests.

For interactive OAuth device-flow login, use the native `@columnar-tech/dbc`
CLI (`dbc auth login`); the Node runtime shares that host's store by default.
Alternatively, inject the resulting token through `credential`.

## Build

The `.wasm` and `wasm_exec.js` are build artifacts. From the repo root:

```sh
node packages/npm-wasm/scripts/build.js --version 0.3.0
node packages/npm-wasm/test/smoke.cjs   # optional smoke test
```

## Links

- [GitHub Repo](https://github.com/columnar-tech/dbc)
- [Issues](https://github.com/columnar-tech/dbc/issues)
