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
use std::path::PathBuf;

/// Client configuration. The default targets Columnar's public registry and
/// uses credentials stored by `dbc auth login`.
#[derive(Debug, Clone, Default)]
pub struct ClientConfig {
    /// Registry base URL; `None` means `https://dbc-cdn.columnar.tech`.
    pub base_url: Option<String>,
    /// Token credential for a private registry, replacing stored credentials
    /// for that registry's host.
    pub credential: Option<Credential>,
}

#[derive(Debug, Clone, Default)]
pub struct Credential {
    pub registry_url: String,
    pub auth_uri: String,
    pub token: Option<String>,
    pub refresh_token: Option<String>,
    pub client_id: Option<String>,
}

/// Where drivers are installed, uninstalled, and listed.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum InstallLocation {
    /// dbc's user-level location, as `dbc install -l user`.
    User,
    /// dbc's system-level location, as `dbc install -l system`; usually needs
    /// elevated privileges.
    System,
    /// An explicit driver directory.
    Directory(PathBuf),
}

/// Options for [`Client::install_with`](crate::Client::install_with). The
/// default verifies the driver's signature.
#[derive(Debug, Clone, Default)]
#[non_exhaustive]
pub struct InstallOptions {
    pub(crate) allow_unsigned: bool,
}

impl InstallOptions {
    /// Skip signature verification, like the CLI's `--no-verify`. Only for
    /// drivers from a trusted private registry.
    pub fn allow_unsigned(mut self, allow: bool) -> Self {
        self.allow_unsigned = allow;
        self
    }
}

#[derive(Debug, Clone)]
#[non_exhaustive]
pub struct Driver {
    pub path: String,
    pub title: String,
    pub license: String,
    pub description: String,
}

#[derive(Debug, Clone)]
#[non_exhaustive]
pub struct SearchResult {
    pub drivers: Vec<Driver>,
    /// Set when some registries failed but others answered (partial result).
    pub warning: Option<String>,
}

#[derive(Debug, Clone)]
#[non_exhaustive]
pub struct Latest {
    pub version: String,
    pub url: String,
}

#[derive(Debug, Clone)]
#[non_exhaustive]
pub struct ResolveResult {
    pub path: String,
    pub platform: String,
    pub versions: Vec<String>,
    /// Newest stable package for `platform`, if any.
    pub latest: Option<Latest>,
}

#[derive(Debug, Clone)]
#[non_exhaustive]
pub struct Manifest {
    pub id: String,
    pub name: String,
    pub version: String,
    pub source: String,
    pub driver_path: String,
}

#[derive(Debug, Clone)]
#[non_exhaustive]
pub struct InstalledDriver {
    pub id: String,
    pub name: String,
    pub version: String,
    /// Where the driver's manifest was installed: a directory, or a registry
    /// key for user/system installs on Windows.
    pub file_path: String,
}
