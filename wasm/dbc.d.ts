// TypeScript declarations for the dbc WASM library.
// Node build (-tags dbcnode) exposes the full API; the browser build exposes
// only search/resolve/verifySignature (install/uninstall/listInstalled need a
// filesystem).

export interface Driver {
  path: string;
  title: string;
  license: string;
  description: string;
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
  token: string;
  refreshToken: string;
  clientID: string;
}

export interface DbcOptions {
  baseURL?: string;
  platform?: string;
  credential?: OAuthCredential;
}

export interface Dbc {
  search(pattern?: string): Promise<Driver[]>;
  resolve(name: string, platform?: string): Promise<ResolveResult>;
  verifySignature(lib: Uint8Array, sig: Uint8Array): Promise<boolean>;

  install?(name: string, location: string): Promise<Manifest>;
  uninstall?(name: string, location: string): Promise<void>;
  listInstalled?(location: string): Promise<InstalledDriver[]>;
}

export function loadDbc(opts?: DbcOptions): Promise<Dbc>;
