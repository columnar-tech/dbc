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

export { SCHEMA_VERSION } from './install.js';
export type { InstallStatus, InstallProgressEvent } from './install.js';
export type { UninstallStatus } from './uninstall.js';
export type { SearchDriverBasic, SearchDriverVerbose, SearchResponse } from './search.js';
export type { DriverInfo } from './info.js';
export type { InitResponse, AddResponse, AddResponseDriver, RemoveResponse, RemoveResponseDriver } from './driver_list.js';
export type { SyncProgressEvent, SyncedDriver, SyncError, SyncStatus } from './sync.js';
export type { AuthDeviceCodeEvent, AuthLoginResponse, AuthLogoutResponse, AuthLicenseInstallResponse, AuthRegistryStatus, AuthStatus } from './auth.js';
export type { ErrorResponse } from './error.js';

export type Envelope =
  | { schema_version: 1; kind: 'install.status'; payload: import('./install.js').InstallStatus }
  | { schema_version: 1; kind: 'install.progress'; payload: import('./install.js').InstallProgressEvent }
  | { schema_version: 1; kind: 'uninstall.status'; payload: import('./uninstall.js').UninstallStatus }
  | { schema_version: 1; kind: 'search.results'; payload: import('./search.js').SearchResponse }
  | { schema_version: 1; kind: 'driver.info'; payload: import('./info.js').DriverInfo }
  | { schema_version: 1; kind: 'init.response'; payload: import('./driver_list.js').InitResponse }
  | { schema_version: 1; kind: 'add.response'; payload: import('./driver_list.js').AddResponse }
  | { schema_version: 1; kind: 'remove.response'; payload: import('./driver_list.js').RemoveResponse }
  | { schema_version: 1; kind: 'sync.progress'; payload: import('./sync.js').SyncProgressEvent }
  | { schema_version: 1; kind: 'sync.status'; payload: import('./sync.js').SyncStatus }
  | { schema_version: 1; kind: 'auth.device_code'; payload: import('./auth.js').AuthDeviceCodeEvent }
  | { schema_version: 1; kind: 'auth.login.success'; payload: import('./auth.js').AuthLoginResponse }
  | { schema_version: 1; kind: 'auth.login.failed'; payload: import('./auth.js').AuthLoginResponse }
  | { schema_version: 1; kind: 'auth.logout'; payload: import('./auth.js').AuthLogoutResponse }
  | { schema_version: 1; kind: 'auth.license_install'; payload: import('./auth.js').AuthLicenseInstallResponse }
  | { schema_version: 1; kind: 'auth.status'; payload: import('./auth.js').AuthStatus }
  | { schema_version: 1; kind: 'error'; payload: import('./error.js').ErrorResponse };

export function isInstallStatus(e: Envelope): e is Extract<Envelope, { kind: 'install.status' }> {
  return e.kind === 'install.status';
}

export function isInstallProgress(e: Envelope): e is Extract<Envelope, { kind: 'install.progress' }> {
  return e.kind === 'install.progress';
}

export function isSyncStatus(e: Envelope): e is Extract<Envelope, { kind: 'sync.status' }> {
  return e.kind === 'sync.status';
}

export function isError(e: Envelope): e is Extract<Envelope, { kind: 'error' }> {
  return e.kind === 'error';
}
