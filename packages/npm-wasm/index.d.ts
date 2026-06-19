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

export interface Driver {
  path: string;
  title: string;
  license: string;
  description: string;
}

export interface SearchResult {
  drivers: Driver[];
  /**
   * Soft-failure channel: set when some registries were unreachable but a
   * partial result is still returned. `search` aggregates across all configured
   * registries, so a partial answer is meaningful; `resolve` targets a single
   * named driver and has no equivalent — it either resolves or rejects.
   */
  warning?: string;
}

export interface ResolveResult {
  path: string;
  platform: string;
  versions: string[];
  latest?: { version: string; url: string };
}

export interface Manifest {
  id: string;
  name: string;
  version: string;
  source: string;
  driverPath: string;
}

export interface InstalledDriver {
  id: string;
  name: string;
  version: string;
  filePath: string;
}

export interface OAuthCredential {
  registryURL: string;
  authURI: string;
  token?: string;
  refreshToken?: string;
  clientID?: string;
}

export interface DbcOptions {
  /** Driver registry base URL. Per-instance: isolated between `loadDbc` calls. */
  baseURL?: string;
  /**
   * Host platform tuple (e.g. "linux_amd64"). Defaults to the detected Node host.
   *
   * NOTE: unlike `baseURL`/`credential`, the in-process backend applies this
   * **process-globally** (it is the last value passed to any `loadDbc` wins).
   * Concurrent in-process instances with different platforms clobber each other.
   * For per-call platform behavior, pass `platform` to `resolve()` instead, which
   * takes precedence over this global default.
   */
  platform?: string;
  /** Credentials for a private registry, injected at runtime. Per-instance. */
  credential?: OAuthCredential;
  /** Run the wasm runtime in a Node worker_threads Worker so heavy
   *  install/PGP work stays off the host event loop. Node-only.
   *  When `true`, `close()` is **mandatory** — the worker thread is not GC-managed,
   *  so a missing `close()` leaks the thread and keeps the Node process alive. */
  worker?: boolean;
}

export interface Dbc {
  search(pattern?: string): Promise<SearchResult>;
  /**
   * Resolve versions + the latest package URL for a driver.
   * @param platform Optional per-call platform tuple. When provided it takes
   *   precedence over `loadDbc({ platform })`; when omitted, the default
   *   platform set at load time is used.
   */
  resolve(name: string, platform?: string): Promise<ResolveResult>;
  /**
   * Verify a driver library's detached signature. Trust is anchored solely to
   * the Columnar signing key (`SignedByColumnar`); per-registry or caller-supplied
   * trust anchors are a deliberate non-goal of this build.
   */
  verifySignature(library: Uint8Array, signature: Uint8Array): Promise<boolean>;
  /**
   * Release the underlying client handle (and, in worker mode, terminate the
   * worker thread). Idempotent. In the in-process backend the handle is also
   * auto-released on GC, so `close()` is optional there; under `worker: true`
   * it is **required** to avoid leaking the worker thread.
   */
  close(): Promise<void>;

  /**
   * Node-only (requires a filesystem).
   *
   * NOTE: `install`/`listInstalled` resolve the package via the **process-global**
   * platform (see `DbcOptions.platform`) and have no per-call platform override.
   * Concurrent in-process clients pinned to different platforms can therefore
   * install or list the wrong platform's artifact; use the worker backend for
   * multi-platform concurrency.
   */
  install?(name: string, location: string): Promise<Manifest>;
  uninstall?(name: string, location: string): Promise<void>;
  listInstalled?(location: string): Promise<InstalledDriver[]>;
}

/** Alias for {@link Dbc} that better signals the handle/lifecycle this object owns. */
export type DbcClient = Dbc;

export function loadDbc(opts?: DbcOptions): Promise<Dbc>;
/**
 * The detected host platform tuple (e.g. "linux_amd64").
 * @throws if `process.platform`/`process.arch` is unsupported.
 */
export function hostPlatformTuple(): string;
/**
 * Low-level path normalizer applied internally to every `location` argument.
 * On non-Windows hosts it is the identity function; on Windows it converts
 * backslashes to forward slashes and makes drive-relative paths absolute.
 *
 * Exposed only for tooling/tests — do NOT pre-normalize a `location` before
 * passing it to `install`/`uninstall`/`listInstalled`, as those already
 * normalize internally (double-normalizing is harmless but pointless).
 */
export function normalizeLocation(loc: string): string;
