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

/// Client configuration. By default, loads the user's global `config.toml`
/// and uses credentials stored by [`login`](crate::login) or `dbc auth login`.
///
/// Build with [`ClientConfig::default()`] and the option methods. An explicit
/// [`base_url`](Self::base_url) or nonempty [`registries`](Self::registries)
/// list replaces all file-based registry configuration; setting both is an
/// error. Without either, project registries take priority over global
/// registries, then dbc's built-in registries. Configuration load errors are
/// returned by [`Client::with_config`](crate::Client::with_config).
#[derive(Debug, Clone, Default)]
#[non_exhaustive]
pub struct ClientConfig {
    /// A single explicit registry URL, mutually exclusive with `registries`.
    pub base_url: Option<String>,
    /// Explicit registries, used verbatim. An empty list uses configuration
    /// files and dbc's built-in registries instead.
    pub registries: Vec<Registry>,
    /// Whether to load the user's global registry configuration.
    pub registry_config: RegistryConfig,
    /// Project `dbc.toml` to load. No project file is discovered automatically.
    pub project_file: Option<PathBuf>,
    /// Whether hosts without an injected credential use stored credentials.
    pub credential_store: CredentialStore,
    /// Token credential replacing stored credentials for its registry's
    /// host. Other hosts still use the store unless it is disabled.
    pub credential: Option<Credential>,
}

impl ClientConfig {
    /// Use a single registry, ignoring global and project configuration.
    pub fn base_url(mut self, url: impl Into<String>) -> Self {
        self.base_url = Some(url.into());
        self
    }

    /// Use an explicit registry list instead of global and project
    /// configuration. Must not be combined with `base_url`.
    pub fn registries(mut self, registries: Vec<Registry>) -> Self {
        self.registries = registries;
        self
    }

    /// Choose whether to load the user's global `config.toml`.
    pub fn registry_config(mut self, registry_config: RegistryConfig) -> Self {
        self.registry_config = registry_config;
        self
    }

    /// Load `registries` and `replace_defaults` from a project `dbc.toml`.
    /// A missing or invalid file is an error unless explicit registries
    /// (or `base_url`) override it.
    pub fn project_file(mut self, path: impl Into<PathBuf>) -> Self {
        self.project_file = Some(path.into());
        self
    }

    /// Choose whether to look up stored credentials for other hosts.
    pub fn credential_store(mut self, credential_store: CredentialStore) -> Self {
        self.credential_store = credential_store;
        self
    }

    /// Inject a token credential for its registry's host.
    pub fn credential(mut self, credential: Credential) -> Self {
        self.credential = Some(credential);
        self
    }
}

/// One registry in an explicit [`ClientConfig::registries`] list.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct Registry {
    pub url: String,
    pub name: Option<String>,
}

impl Registry {
    pub fn new(url: impl Into<String>) -> Self {
        Self { url: url.into(), name: None }
    }

    pub fn name(mut self, name: impl Into<String>) -> Self {
        self.name = Some(name.into());
        self
    }
}

/// Whether to load the user's global registry configuration.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum RegistryConfig {
    /// Load `config.toml` from the same directory as the dbc CLI. A missing
    /// file uses built-in defaults; an unreadable or invalid file is an error.
    #[default]
    Default,
    /// Skip the global file. An explicitly selected project file still applies.
    None,
}

/// Whether a client uses dbc's stored credentials to authenticate.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum CredentialStore {
    /// Use stored credentials for hosts without an injected credential.
    /// Cached lookups may miss external store changes until the process restarts.
    #[default]
    Default,
    /// Stored credentials are never used to authenticate. An injected
    /// credential still works; refreshing it can still update dbc's
    /// credential/license files.
    None,
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
