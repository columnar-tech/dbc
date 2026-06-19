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
  /** Driver registry base URL. Defaults to the public Columnar CDN + private registry. */
  baseURL?: string;
  /** Host platform tuple (e.g. "linux_amd64"). Defaults to the detected Node host. */
  platform?: string;
  /** Credentials for a private registry, injected at runtime. */
  credential?: OAuthCredential;
}

export interface Dbc {
  search(pattern?: string): Promise<SearchResult>;
  resolve(name: string, platform?: string): Promise<ResolveResult>;
  verifySignature(lib: Uint8Array, sig: Uint8Array): Promise<boolean>;
  /** Release the underlying client handle. Optional; also auto-released on GC. */
  close(): void;

  /** Node-only (requires a filesystem). */
  install?(name: string, location: string): Promise<Manifest>;
  uninstall?(name: string, location: string): Promise<void>;
  listInstalled?(location: string): Promise<InstalledDriver[]>;
}

export function loadDbc(opts?: DbcOptions): Promise<Dbc>;
export function hostPlatformTuple(): string;
