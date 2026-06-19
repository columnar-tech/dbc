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
install/uninstall ADBC drivers to disk, and verify signatures, without spawning
a subprocess.

> For the command-line tool, use [`@columnar-tech/dbc`](https://www.npmjs.com/package/@columnar-tech/dbc) instead.

## Requirements

- Node.js >= 18 (uses global `fetch`; the loader wires Node's `fs`/`process`/`webcrypto` into the wasm runtime automatically).

## Platform support

Linux and macOS are validated. **Windows host support is experimental and not yet
verified on a Windows runtime.** The loader normalizes Windows paths (backslashes
to forward slashes; drive-relative to absolute), and the Go config layer no longer
splits a drive-lettered `location` on `:`. Known Windows caveats:

- Pass an explicit `location` (the npm API already does). Registry-backed
  user/system config levels are unavailable under WASM.
- The manifest-symlink compatibility shim (for the ADBC Python driver-manager
  <= 1.8.0) is inactive without Developer Mode; use driver-manager >= 1.8.1.
- Not yet verified on a Windows runtime: `os.MkdirAll`/`os.OpenRoot` over a drive
  root through Node's `fs`. Validate on a Windows runner before relying on it.

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

CommonJS works too:

```js
const { loadDbc } = require("@columnar-tech/dbc-wasm");
```

### Options

```js
await loadDbc({
  baseURL: "https://my-registry.example.com", // override the default registries
  platform: "linux_amd64",                    // defaults to the detected Node host
  credential: {                               // private-registry auth (OAuth refresh)
    registryURL: "https://my-registry.example.com",
    authURI: "https://my-registry.example.com",
    token: "...",
    refreshToken: "...",
    clientID: "...",
  },
  worker: true,                               // run the wasm in a Node Worker Thread
});
```

Pass `worker: true` to run the runtime in a Node `worker_threads` Worker, so large
installs and signature verification stay off your app's event loop. Call
`dbc.close()` when done to terminate the worker (it also releases the client handle).

For OAuth device-flow login, run the native `@columnar-tech/dbc` CLI
(`dbc auth login`) once; the WASM build reads and refreshes the stored
credentials, or you can inject a token via the `credential` option above.

## Build

The `.wasm` and `wasm_exec.js` are build artifacts. From the repo root:

```sh
node packages/npm-wasm/scripts/build.js --version 0.3.0
node packages/npm-wasm/test/smoke.cjs   # optional smoke test
```

## Links

- [GitHub Repo](https://github.com/columnar-tech/dbc)
- [Issues](https://github.com/columnar-tech/dbc/issues)
