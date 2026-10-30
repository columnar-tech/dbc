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
mod common;

use columnar_dbc::{list_installed, uninstall, Client, ClientConfig, Credential, Error, InstallLocation, InstallOptions};
use std::path::{Path, PathBuf};

fn temp_location() -> (tempfile::TempDir, InstallLocation) {
    let dir = tempfile::tempdir().unwrap();
    let location = InstallLocation::Directory(dir.path().to_path_buf());
    (dir, location)
}

#[test]
fn search_resolve_install_list_uninstall() {
    let client = common::client();

    let search = client.search("test-driver-1").unwrap();
    assert_eq!(search.drivers.len(), 1, "{search:?}");
    assert_eq!(search.drivers[0].path, "test-driver-1");
    assert_eq!(search.drivers[0].title, "Test Driver 1");
    assert_eq!(search.warning, None);

    let resolved = client.resolve("test-driver-1", None).unwrap();
    assert_eq!(resolved.path, "test-driver-1");
    assert_eq!(resolved.versions.len(), 1, "{resolved:?}");
    assert!(resolved.latest.as_ref().is_some_and(|l| l.url.ends_with(".tar.gz")), "{resolved:?}");

    let (_dir, location) = temp_location();
    let manifest = client.install("test-driver-1", &location).unwrap();
    assert_eq!(manifest.id, "test-driver-1");
    assert!(Path::new(&manifest.driver_path).exists(), "{manifest:?}");

    let installed = list_installed(&location).unwrap();
    assert_eq!(installed.len(), 1);
    assert_eq!(installed[0].id, "test-driver-1");

    uninstall("test-driver-1", &location).unwrap();
    assert!(list_installed(&location).unwrap().is_empty());
}

#[test]
fn install_rejects_a_bad_signature_unless_allowed() {
    let client = common::client();

    let (_dir, location) = temp_location();
    match client.install("test-driver-tampered", &location) {
        Err(Error::Dbc(msg)) => assert!(msg.contains("signature verification failed"), "{msg}"),
        other => panic!("expected a signature error, got {other:?}"),
    }
    assert!(list_installed(&location).unwrap().is_empty(), "a rejected driver must not stay installed");

    let options = InstallOptions::default().allow_unsigned(true);
    let manifest = client.install_with("test-driver-tampered", &location, &options).unwrap();
    assert!(Path::new(&manifest.driver_path).exists(), "{manifest:?}");
}

#[test]
fn go_errors_surface_as_dbc_errors() {
    match common::client().resolve("nonexistent-driver", Some("linux_amd64")) {
        Err(Error::Dbc(msg)) => assert_eq!(msg, r#"driver "nonexistent-driver" not found"#),
        other => panic!("expected Error::Dbc, got {other:?}"),
    }
}

#[test]
fn invalid_credential_is_rejected_at_construction() {
    let cfg = ClientConfig {
        base_url: None,
        credential: Some(Credential {
            registry_url: "http://\u{1}".into(),
            auth_uri: "http://example.test".into(),
            token: Some("t".into()),
            ..Default::default()
        }),
    };
    match Client::with_config(&cfg) {
        Err(Error::Dbc(msg)) => assert!(msg.contains("invalid credential registryURL"), "{msg}"),
        Err(other) => panic!("expected Error::Dbc, got {other:?}"),
        Ok(_) => panic!("expected an error"),
    }
}

#[test]
fn unrepresentable_arguments_are_invalid() {
    assert!(matches!(common::client().search("a\0b"), Err(Error::InvalidArgument(_))));

    #[cfg(unix)]
    let bad: PathBuf = {
        use std::os::unix::ffi::OsStrExt;
        PathBuf::from(std::ffi::OsStr::from_bytes(b"/tmp/\xff"))
    };
    #[cfg(windows)]
    let bad: PathBuf = {
        use std::os::windows::ffi::OsStringExt;
        PathBuf::from(std::ffi::OsString::from_wide(&[0xD800]))
    };
    assert!(matches!(list_installed(&InstallLocation::Directory(bad)), Err(Error::InvalidArgument(_))));
}
