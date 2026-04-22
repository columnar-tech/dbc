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

export interface AuthDeviceCodeEvent {
  verification_uri: string;
  verification_uri_complete: string;
  user_code: string;
  expires_in: number;
  interval: number;
}

export interface AuthLoginResponse {
  status: string;
  registry: string;
  message?: string;
}

export interface AuthLogoutResponse {
  status: string;
  registry: string;
}

export interface AuthLicenseInstallResponse {
  status: string;
  license_path: string;
}

export interface AuthRegistryStatus {
  url: string;
  authenticated: boolean;
  auth_type?: string;
  license_valid: boolean;
}

export interface AuthStatus {
  registries: AuthRegistryStatus[];
}
