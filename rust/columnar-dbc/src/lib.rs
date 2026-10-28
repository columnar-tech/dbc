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
//! Rust bindings for [dbc](https://columnar.tech/dbc): search, resolve, and
//! install ADBC drivers.
//!
//! dbc is statically linked through `columnar-dbc-sys`; no `dbc` executable
//! or Go toolchain is needed to build or run. Every call blocks the calling
//! thread on network/filesystem I/O; from async code, run it on a blocking
//! thread (e.g. `tokio::task::spawn_blocking`).

mod ffi;
mod types;

pub use types::*;

use columnar_dbc_sys as sys;
use ffi::{check, cstring, ptr_or_null, List};
use std::ffi::{c_char, CString};
use std::fmt;
use std::path::Path;
use std::ptr;

const USER_AGENT_SUFFIX: &str = concat!("columnar-dbc-rust/", env!("CARGO_PKG_VERSION"));

pub type Result<T, E = Error> = std::result::Result<T, E>;

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
    /// Client for Columnar's public registry and stored credentials.
    pub fn new() -> Result<Self> {
        Self::with_config(&ClientConfig::default())
    }

    pub fn with_config(config: &ClientConfig) -> Result<Self> {
        // Keys are the option names documented in libdbc.h.
        let mut options: Vec<(&str, &str)> = vec![("user_agent_suffix", USER_AGENT_SUFFIX)];
        if let Some(url) = &config.base_url {
            options.push(("base_url", url.as_str()));
        }
        if let Some(c) = &config.credential {
            options.push(("credential.registry_url", c.registry_url.as_str()));
            options.push(("credential.auth_uri", c.auth_uri.as_str()));
            for (key, value) in [
                ("credential.token", &c.token),
                ("credential.refresh_token", &c.refresh_token),
                ("credential.client_id", &c.client_id),
            ] {
                if let Some(value) = value {
                    options.push((key, value.as_str()));
                }
            }
        }
        let keys = options.iter().map(|(k, _)| cstring(k)).collect::<Result<Vec<_>>>()?;
        let values = options.iter().map(|(_, v)| cstring(v)).collect::<Result<Vec<_>>>()?;
        let mut key_ptrs: Vec<*mut c_char> = keys.iter().map(|k| k.as_ptr().cast_mut()).collect();
        let mut value_ptrs: Vec<*mut c_char> = values.iter().map(|v| v.as_ptr().cast_mut()).collect();

        let mut client = ptr::null_mut();
        // SAFETY: both arrays hold `len` strings that outlive the call and are
        // only read; `client` is written only on DBC_OK.
        check(|err| unsafe {
            sys::dbc_client_new(key_ptrs.as_mut_ptr(), value_ptrs.as_mut_ptr(), key_ptrs.len(), &mut client, err)
        })?;
        Ok(Self { ptr: client })
    }

    /// Drivers whose name matches `pattern` (empty = all) across registries.
    pub fn search(&self, pattern: &str) -> Result<SearchResult> {
        let pattern = cstring(pattern)?;
        // SAFETY: live client; the pattern is only read during the call.
        let list = List::from_call(|out, err| unsafe { sys::dbc_search(self.ptr, pattern.as_ptr().cast_mut(), out, err) })?;
        let drivers = (0..list.len())
            .map(|i| Driver {
                path: list.get(i, sys::DBC_FIELD_ID),
                title: list.get(i, sys::DBC_FIELD_NAME),
                license: list.get(i, sys::DBC_FIELD_LICENSE),
                description: list.get(i, sys::DBC_FIELD_DESCRIPTION),
            })
            .collect();
        Ok(SearchResult { drivers, warning: list.info(sys::DBC_FIELD_WARNING) })
    }

    /// Versions of `name` for `platform` (`None` = host, e.g. `linux_amd64`).
    pub fn resolve(&self, name: &str, platform: Option<&str>) -> Result<ResolveResult> {
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

    /// Installs `name` at `location`, verifying its signature with Columnar's
    /// key; a driver that fails verification is removed and an error returned.
    pub fn install(&self, name: &str, location: &InstallLocation) -> Result<Manifest> {
        self.install_with(name, location, &InstallOptions::default())
    }

    /// [`install`](Self::install) with options, e.g. to allow unsigned drivers.
    pub fn install_with(&self, name: &str, location: &InstallLocation, options: &InstallOptions) -> Result<Manifest> {
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
pub fn uninstall(name: &str, location: &InstallLocation) -> Result<()> {
    let name = cstring(name)?;
    let target = Target::new(location)?;
    // SAFETY: inputs are only read during the call.
    check(|err| unsafe { sys::dbc_uninstall(name.as_ptr().cast_mut(), target.kind, target.directory(), err) })
}

/// Drivers installed at `location`, sorted by id.
pub fn list_installed(location: &InstallLocation) -> Result<Vec<InstalledDriver>> {
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

/// An [`InstallLocation`] converted for the C API.
struct Target {
    kind: sys::dbc_target_kind,
    directory: Option<CString>,
}

impl Target {
    fn new(location: &InstallLocation) -> Result<Self> {
        Ok(match location {
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

fn path_str(path: &Path) -> Result<&str> {
    path.to_str()
        .ok_or_else(|| Error::InvalidArgument(format!("path {} is not valid UTF-8", path.display())))
}
