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
#![doc = include_str!("../README.md")]

mod ffi;
mod types;

pub use types::*;

use columnar_dbc_sys as sys;
use ffi::{check, cstring, ptr_or_null, List};
use std::borrow::Cow;
use std::ffi::{c_char, CStr, CString};
use std::fmt;
use std::path::{Path, PathBuf};
use std::ptr;

const USER_AGENT_SUFFIX: &str = concat!("columnar-dbc-rust/", env!("CARGO_PKG_VERSION"));

#[derive(Debug)]
pub enum Error {
    /// dbc reported a failure (network, registry, filesystem, signature, ...).
    Dbc(String),
    /// An argument cannot be passed to dbc (a NUL byte or a non-UTF-8 path).
    InvalidArgument(String),
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Error::Dbc(m) => write!(f, "dbc: {m}"),
            Error::InvalidArgument(m) => write!(f, "invalid argument: {m}"),
        }
    }
}

impl std::error::Error for Error {}

/// A dbc client bound to one registry configuration. `Send` but not `Sync`:
/// use it from one thread at a time.
pub struct Client {
    ptr: *mut sys::dbc_client,
}

// SAFETY: a dbc_client has no thread affinity; it is only unsynchronized,
// which the raw pointer field already makes !Sync.
unsafe impl Send for Client {}

impl Client {
    /// Client using global registry configuration and stored credentials.
    /// With no global file, uses dbc's built-in registries. Invalid or
    /// unreadable global configuration is an error.
    pub fn new() -> Result<Self, Error> {
        Self::with_config(&ClientConfig::default())
    }

    /// Creates a client with explicit options. A nonempty `base_url` or explicit
    /// `registries` list (even empty) overrides global and project files
    /// without reading them; combining both explicit options is an error.
    pub fn with_config(config: &ClientConfig) -> Result<Self, Error> {
        let option_key = |key: &'static CStr| Cow::Borrowed(key);
        let capacity = 11 + 2 * config.registries.as_ref().map_or(0, Vec::len);
        let mut options = Vec::with_capacity(capacity);
        let mut option = |key: Cow<'static, CStr>, value: &str| -> Result<(), Error> {
            options.push((key, cstring(value)?));
            Ok(())
        };
        option(option_key(sys::DBC_OPTION_USER_AGENT_SUFFIX), USER_AGENT_SUFFIX)?;
        option(
            option_key(sys::DBC_OPTION_REGISTRY_CONFIG),
            match config.registry_config {
                RegistryConfig::Default => "default",
                RegistryConfig::None => "none",
            },
        )?;
        option(
            option_key(sys::DBC_OPTION_CREDENTIAL_STORE),
            match config.credential_store {
                CredentialStore::Default => "default",
                CredentialStore::None => "none",
            },
        )?;
        if let Some(url) = &config.base_url {
            option(option_key(sys::DBC_OPTION_BASE_URL), url)?;
        }
        if let Some(registries) = &config.registries {
            let registry_prefix = sys::DBC_OPTION_REGISTRY_PREFIX.to_str().expect("registry prefix is UTF-8");
            for (i, registry) in registries.iter().enumerate() {
                option(cstring(format!("{registry_prefix}{i}.url"))?.into(), &registry.url)?;
                if let Some(name) = &registry.name {
                    option(cstring(format!("{registry_prefix}{i}.name"))?.into(), name)?;
                }
            }
        }
        if config.base_url.as_deref().unwrap_or_default().is_empty() && config.registries.is_none() {
            if let Some(path) = &config.project_file {
                option(option_key(sys::DBC_OPTION_PROJECT_FILE), path_str(path)?)?;
            }
        }
        if let Some(c) = &config.credential {
            option(option_key(sys::DBC_OPTION_CREDENTIAL_REGISTRY_URL), &c.registry_url)?;
            option(option_key(sys::DBC_OPTION_CREDENTIAL_AUTH_URI), &c.auth_uri)?;
            for (key, value) in [
                (sys::DBC_OPTION_CREDENTIAL_TOKEN, &c.token),
                (sys::DBC_OPTION_CREDENTIAL_REFRESH_TOKEN, &c.refresh_token),
                (sys::DBC_OPTION_CREDENTIAL_CLIENT_ID, &c.client_id),
            ] {
                if let Some(value) = value {
                    option(option_key(key), value)?;
                }
            }
        }
        if let Some(registries) = &config.registries {
            options.push((option_key(sys::DBC_OPTION_REGISTRY_COUNT), cstring(registries.len().to_string())?));
        }
        let mut key_ptrs: Vec<*mut c_char> = options.iter().map(|(k, _)| k.as_ptr().cast_mut()).collect();
        let mut value_ptrs: Vec<*mut c_char> = options.iter().map(|(_, v)| v.as_ptr().cast_mut()).collect();

        let mut client = ptr::null_mut();
        // SAFETY: both arrays hold `len` strings that outlive the call and are
        // only read; `client` is written only on DBC_OK.
        check(|err| unsafe {
            sys::dbc_client_new(key_ptrs.as_mut_ptr(), value_ptrs.as_mut_ptr(), key_ptrs.len(), &mut client, err)
        })?;
        Ok(Self { ptr: client })
    }

    /// Drivers whose name matches `pattern` (empty = all) across registries.
    pub fn search(&self, pattern: &str) -> Result<SearchResult, Error> {
        let pattern = cstring(pattern)?;
        // SAFETY: live client; the pattern is only read during the call.
        let list = List::from_call(|out, err| unsafe { sys::dbc_search(self.ptr, pattern.as_ptr().cast_mut(), out, err) })?;
        let drivers = (0..list.len())
            .map(|i| Driver {
                path: list.get(i, sys::DBC_FIELD_ID),
                title: list.get(i, sys::DBC_FIELD_NAME),
                license: list.get(i, sys::DBC_FIELD_LICENSE),
                description: list.get(i, sys::DBC_FIELD_DESCRIPTION),
                docs_url: list.get_opt(i, sys::DBC_FIELD_DOCS_URL),
                registry: list.get(i, sys::DBC_FIELD_REGISTRY),
            })
            .collect();
        Ok(SearchResult { drivers, warning: list.info(sys::DBC_FIELD_WARNING) })
    }

    /// Versions of `name` for `platform` (`None` = host, e.g. `linux_amd64`).
    pub fn resolve(&self, name: &str, platform: Option<&str>) -> Result<ResolveResult, Error> {
        let name = cstring(name)?;
        let platform = platform.map(cstring).transpose()?;
        // SAFETY: as in `search`; a NULL platform means the host.
        let list = List::from_call(|out, err| unsafe {
            sys::dbc_resolve(self.ptr, name.as_ptr().cast_mut(), ptr_or_null(&platform), out, err)
        })?;
        let latest = list.info(sys::DBC_FIELD_LATEST_VERSION).map(|version| Latest {
            version,
            url: list.info(sys::DBC_FIELD_LATEST_URL).unwrap_or_default(),
        });
        Ok(ResolveResult {
            path: list.info(sys::DBC_FIELD_ID).unwrap_or_default(),
            platform: list.info(sys::DBC_FIELD_PLATFORM).unwrap_or_default(),
            versions: (0..list.len()).map(|i| list.get(i, sys::DBC_FIELD_VERSION)).collect(),
            latest,
        })
    }

    /// Installs `name` where `dbc install` would without `-l` (see
    /// [`InstallLocation::Default`]), verifying its signature with Columnar's
    /// key; a driver that fails verification is removed and an error returned.
    pub fn install(&self, name: &str) -> Result<Manifest, Error> {
        self.install_at(name, &InstallLocation::Default)
    }

    /// [`install`](Self::install) into `location`.
    pub fn install_at(&self, name: &str, location: &InstallLocation) -> Result<Manifest, Error> {
        self.install_with(name, location, &InstallOptions::default())
    }

    /// [`install_at`](Self::install_at) with options, e.g. to allow unsigned
    /// drivers.
    pub fn install_with(&self, name: &str, location: &InstallLocation, options: &InstallOptions) -> Result<Manifest, Error> {
        let name = cstring(name)?;
        let target = Target::new(location)?;
        let flags = if options.allow_unsigned { sys::DBC_INSTALL_ALLOW_UNSIGNED } else { 0 };
        // SAFETY: as in `search`.
        let list = List::from_call(|out, err| unsafe {
            sys::dbc_install(self.ptr, name.as_ptr().cast_mut(), target.kind, target.directory(), flags, out, err)
        })?;
        Ok(Manifest {
            id: list.get(0, sys::DBC_FIELD_ID),
            name: list.get(0, sys::DBC_FIELD_NAME),
            version: list.get(0, sys::DBC_FIELD_VERSION),
            source: list.get(0, sys::DBC_FIELD_SOURCE),
            driver_path: list.get(0, sys::DBC_FIELD_DRIVER_PATH),
        })
    }
}

impl Drop for Client {
    fn drop(&mut self) {
        // SAFETY: `ptr` came from dbc_client_new and is freed exactly once.
        unsafe { sys::dbc_client_free(self.ptr) }
    }
}

/// Removes driver `name` from `location`.
pub fn uninstall(name: &str, location: &InstallLocation) -> Result<(), Error> {
    let name = cstring(name)?;
    let target = Target::new(location)?;
    // SAFETY: inputs are only read during the call.
    check(|err| unsafe { sys::dbc_uninstall(name.as_ptr().cast_mut(), target.kind, target.directory(), err) })
}

/// Drivers installed at `location`, sorted by id.
pub fn list_installed(location: &InstallLocation) -> Result<Vec<InstalledDriver>, Error> {
    let target = Target::new(location)?;
    // SAFETY: inputs are only read during the call.
    let list = List::from_call(|out, err| unsafe { sys::dbc_list_installed(target.kind, target.directory(), out, err) })?;
    Ok((0..list.len())
        .map(|i| InstalledDriver {
            id: list.get(i, sys::DBC_FIELD_ID),
            name: list.get(i, sys::DBC_FIELD_NAME),
            version: list.get(i, sys::DBC_FIELD_VERSION),
            file_path: list.get(i, sys::DBC_FIELD_LOCATION),
        })
        .collect())
}

/// Saves a token credential in dbc's credential store, replacing any entry
/// for the same registry host. Does not perform an interactive login.
pub fn login(credential: &Credential) -> Result<(), Error> {
    let registry_url = cstring(credential.registry_url.as_str())?;
    let auth_uri = cstring(credential.auth_uri.as_str())?;
    let token = cstring(credential.token.as_deref().unwrap_or_default())?;
    let refresh_token = credential.refresh_token.as_deref().map(cstring).transpose()?;
    let client_id = credential.client_id.as_deref().map(cstring).transpose()?;
    // SAFETY: every input is read only during the call; optional inputs may
    // be NULL and all strings remain live until the call returns.
    check(|err| unsafe {
        sys::dbc_login(
            registry_url.as_ptr().cast_mut(),
            auth_uri.as_ptr().cast_mut(),
            token.as_ptr().cast_mut(),
            ptr_or_null(&refresh_token),
            ptr_or_null(&client_id),
            err,
        )
    })
}

/// Removes the stored credential for `registry_url`'s host. Returns an error
/// when no credential exists for that host.
pub fn logout(registry_url: &str) -> Result<(), Error> {
    let registry_url = cstring(registry_url)?;
    // SAFETY: the URL is only read during the call.
    check(|err| unsafe { sys::dbc_logout(registry_url.as_ptr().cast_mut(), err) })
}

/// Returns the destination of dbc's `columnar.lic`, beside its credential
/// store. The license does not have to exist; this does not install one.
pub fn license_path() -> Result<PathBuf, Error> {
    // SAFETY: the C API writes the owned result and error through `List`.
    let list = List::from_call(|out, err| unsafe { sys::dbc_license_path(out, err) })?;
    list.info(sys::DBC_FIELD_LOCATION)
        .map(PathBuf::from)
        .ok_or_else(|| Error::Dbc("dbc did not return a license path".into()))
}

/// Copies a license to [`license_path`]. Unless `force` is true, `src_path`
/// must be named `columnar.lic` and the destination must not already exist.
/// Setting `force` allows a different filename and overwrites a license.
pub fn install_license(src_path: &Path, force: bool) -> Result<(), Error> {
    let src_path = cstring(path_str(src_path)?)?;
    let flags: sys::dbc_license_flags = if force { sys::DBC_LICENSE_FORCE } else { 0 };
    // SAFETY: the source path is only read during the call.
    check(|err| unsafe { sys::dbc_install_license(src_path.as_ptr().cast_mut(), flags, err) })
}

/// An [`InstallLocation`] converted for the C API.
struct Target {
    kind: sys::dbc_target_kind,
    directory: Option<CString>,
}

impl Target {
    fn new(location: &InstallLocation) -> Result<Self, Error> {
        Ok(match location {
            InstallLocation::Default => Self { kind: sys::DBC_TARGET_DEFAULT, directory: None },
            InstallLocation::User => Self { kind: sys::DBC_TARGET_USER, directory: None },
            InstallLocation::System => Self { kind: sys::DBC_TARGET_SYSTEM, directory: None },
            InstallLocation::Directory(dir) => {
                Self { kind: sys::DBC_TARGET_DIRECTORY, directory: Some(cstring(path_str(dir)?)?) }
            }
        })
    }

    fn directory(&self) -> *mut c_char {
        ptr_or_null(&self.directory)
    }
}

fn path_str(path: &Path) -> Result<&str, Error> {
    path.to_str()
        .ok_or_else(|| Error::InvalidArgument(format!("path is not valid UTF-8: {}", path.display())))
}
