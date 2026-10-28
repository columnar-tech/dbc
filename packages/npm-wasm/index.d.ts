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

export interface Registry {
  url: string;
  name?: string;
}

export interface OAuthCredential {
  registryURL: string;
  authURI: string;
  token?: string;
  refreshToken?: string;
  clientID?: string;
}

export interface DbcOptions {
  /**
   * Use one explicit registry, ignoring registryConfig and projectFile without
   * reading either file. Mutually exclusive with registries (including []).
   * Without either explicit option, Node honors the user's global config.toml
   * and optional projectFile, with the built-in registries as defaults.
   */
  baseURL?: string;
  /**
   * Explicit registry list, used verbatim rather than merged with config files.
   * An empty list disables all registries. Mutually exclusive with baseURL.
   * Per-instance, like baseURL; registryConfig and projectFile are ignored.
   */
  registries?: Registry[];
  /**
   * Node: omitted or "default" honors the user's global dbc config.toml.
   * Uses the native CLI's host paths: AppData/Columnar/dbc on Windows,
   * HOME/Library/Application Support/Columnar/dbc on macOS (both ignore
   * XDG_CONFIG_HOME), and XDG_CONFIG_HOME/columnar/dbc or
   * HOME/.config/columnar/dbc on Unix.
   * A missing global file is allowed; other load errors reject loadDbc.
   * "none" skips the global file, but does not disable an explicit projectFile.
   *
   * The experimental browser build defaults to "none"; explicitly requesting
   * "default" is Node-only and rejects unless baseURL/registries takes precedence.
   */
  registryConfig?: "default" | "none";
  /**
   * Node-only path to a project dbc.toml. Reads registries and replace_defaults;
   * a missing file rejects loadDbc. Ignored when baseURL/registries is supplied.
   * The experimental browser build rejects it unless an explicit registry
   * option takes precedence.
   */
  projectFile?: string;
  /**
   * Driver installation target (e.g. "linux_amd64"), defaulting to the Node host.
   * Does not change the host OS used for config, credential, or license paths.
   *
   * NOTE: unlike `baseURL`/`credential`, the in-process backend applies this
   * **process-globally** (it is the last value passed to any `loadDbc` wins).
   * Concurrent in-process instances with different platforms clobber each other.
   * For per-call platform behavior, pass `platform` to `resolve()` instead, which
   * takes precedence over this global default.
   */
  platform?: string;
  /**
   * Inject a credential for its registry host, replacing that host's stored
   * credential. Other hosts still use the store unless credentialStore is "none".
   * Per-instance; refreshed tokens are retained for subsequent calls.
   */
  credential?: OAuthCredential;
  /**
   * Omitted or "default" reads dbc's stored credentials.
   * Shares the native CLI's host store, including an absolute XDG_DATA_HOME
   * override. Set path environment variables before the runtime starts.
   * Other running runtimes may cache stale lookups: recreate workers or restart
   * the in-process host; creating another in-process client does not reload them.
   * "none": stored credentials are never used to authenticate; refreshing an
   * injected credential can still update dbc's credential/license files.
   * Injected credentials still work with "none"; other hosts have no fallback.
   * This does not disable explicit login/logout/license operations.
   */
  credentialStore?: "default" | "none";
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
   * `install` verifies the driver's signature with Columnar's key; a driver
   * that fails verification is removed and the call rejects. `allowUnsigned`
   * skips verification (like the CLI's `--no-verify`); use it only for
   * drivers from a trusted private registry.
   *
   * NOTE: `install`/`listInstalled` resolve the package via the **process-global**
   * platform (see `DbcOptions.platform`) and have no per-call platform override.
   * Concurrent in-process clients pinned to different platforms can therefore
   * install or list the wrong platform's artifact; use the worker backend for
   * multi-platform concurrency.
   */
  install?(name: string, location: string, options?: { allowUnsigned?: boolean }): Promise<Manifest>;
  uninstall?(name: string, location: string): Promise<void>;
  listInstalled?(location: string): Promise<InstalledDriver[]>;

  /**
   * Node-only: persist a token credential, overwriting any credential for the
   * same registry host. Does not perform an interactive OAuth/device login.
   * The user store is shared, not scoped to this client or credentialStore.
   */
  login?(credential: OAuthCredential): Promise<void>;
  /**
   * Node-only: remove the stored credential for registryURL's host.
   * Rejects if no credential is stored for that host.
   */
  logout?(registryURL: string): Promise<void>;
  /** Node-only: return the user's columnar.lic path without creating a file. */
  licensePath?(): Promise<string>;
  /**
   * Node-only: copy a local license into the shared user store. The source must
   * be named columnar.lic and the destination must not exist unless force is true.
   * Copies the bytes as supplied; this does not validate license contents.
   */
  installLicense?(path: string, options?: { force?: boolean }): Promise<void>;
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
 * Low-level path normalizer applied internally to filesystem path arguments.
 * On non-Windows hosts it is the identity function; on Windows it converts
 * backslashes to forward slashes and makes drive-relative paths absolute.
 *
 * Exposed only for tooling/tests — do NOT pre-normalize paths passed to
 * install/uninstall/listInstalled/installLicense or projectFile, as those
 * already normalize internally (double-normalizing is harmless but pointless).
 */
export function normalizeLocation(loc: string): string;
