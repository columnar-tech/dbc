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

use columnar_dbc::{
    install_license, license_path, list_installed, login, logout, uninstall, Client, ClientConfig, Credential,
    CredentialStore, Error, InstallLocation, InstallOptions, Registry, RegistryConfig,
};
use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

/// Go snapshots the environment when the native runtime loads, before a
/// test can set it. Start a fresh process with every user path isolated.
fn isolated(test: &str) -> Option<PathBuf> {
    if std::env::var("DBC_RUST_TEST_CHILD").as_deref() == Ok(test) {
        return Some(PathBuf::from(std::env::var_os("DBC_RUST_TEST_ROOT").unwrap()));
    }
    let root = tempfile::tempdir().unwrap();
    for directory in ["home", "config", "data", "appdata", "localappdata"] {
        fs::create_dir_all(root.path().join(directory)).unwrap();
    }
    let output = Command::new(std::env::current_exe().unwrap())
        .args(["--exact", test, "--nocapture"])
        .env("DBC_RUST_TEST_CHILD", test)
        .env("DBC_RUST_TEST_ROOT", root.path())
        .env("HOME", root.path().join("home"))
        .env("USERPROFILE", root.path().join("home"))
        .env("XDG_CONFIG_HOME", root.path().join("config"))
        .env("XDG_DATA_HOME", root.path().join("data"))
        .env("APPDATA", root.path().join("appdata"))
        .env("LOCALAPPDATA", root.path().join("localappdata"))
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "isolated {test} failed:\n{}\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr),
    );
    None
}

fn global_config_dir(root: &Path) -> PathBuf {
    if cfg!(target_os = "macos") {
        root.join("home/Library/Application Support/Columnar/dbc")
    } else if cfg!(windows) {
        root.join("appdata/Columnar/dbc")
    } else {
        root.join("config/columnar/dbc")
    }
}

fn write_global_config(root: &Path, contents: &str) {
    let dir = global_config_dir(root);
    fs::create_dir_all(&dir).unwrap();
    fs::write(dir.join("config.toml"), contents).unwrap();
}

fn assert_isolated_license_path(root: &Path) -> PathBuf {
    let path = license_path().unwrap();
    assert!(path.starts_with(root), "license path escaped the isolated environment: {path:?}");
    assert_eq!(path, root.join("data/dbc/credentials/columnar.lic"));
    path
}

fn temp_location() -> (tempfile::TempDir, InstallLocation) {
    let dir = tempfile::tempdir().unwrap();
    let location = InstallLocation::Directory(dir.path().to_path_buf());
    (dir, location)
}

#[test]
fn search_resolve_install_list_uninstall() {
    let Some(_root) = isolated("search_resolve_install_list_uninstall") else { return };
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
    let Some(_root) = isolated("install_rejects_a_bad_signature_unless_allowed") else { return };
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
    let Some(_root) = isolated("go_errors_surface_as_dbc_errors") else { return };
    match common::client().resolve("nonexistent-driver", Some("linux_amd64")) {
        Err(Error::Dbc(msg)) => assert_eq!(msg, r#"driver "nonexistent-driver" not found"#),
        other => panic!("expected Error::Dbc, got {other:?}"),
    }
}

#[test]
fn invalid_credential_is_rejected_at_construction() {
    let Some(_root) = isolated("invalid_credential_is_rejected_at_construction") else { return };
    let cfg = ClientConfig::default().registry_config(RegistryConfig::None).credential(Credential {
        registry_url: "http://\u{1}".into(),
        auth_uri: "http://example.test".into(),
        token: Some("t".into()),
        ..Default::default()
    });
    match Client::with_config(&cfg) {
        Err(Error::Dbc(msg)) => assert!(msg.contains("invalid credential registryURL"), "{msg}"),
        Err(other) => panic!("expected Error::Dbc, got {other:?}"),
        Ok(_) => panic!("expected an error"),
    }
}

#[test]
fn unrepresentable_arguments_are_invalid() {
    let Some(_root) = isolated("unrepresentable_arguments_are_invalid") else { return };
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
    assert!(matches!(list_installed(&InstallLocation::Directory(bad.clone())), Err(Error::InvalidArgument(_))));
    assert!(matches!(install_license(&bad, false), Err(Error::InvalidArgument(_))));
    assert!(matches!(
        Client::with_config(&ClientConfig::default().registry_config(RegistryConfig::None).project_file(&bad)),
        Err(Error::InvalidArgument(_))
    ));
    assert!(Client::with_config(&ClientConfig::default().base_url(common::serve()).project_file(&bad)).is_ok());

    for credential in [
        Credential { registry_url: "http://a\0b".into(), ..Default::default() },
        Credential { auth_uri: "http://a\0b".into(), ..Default::default() },
        Credential { token: Some("a\0b".into()), ..Default::default() },
        Credential { refresh_token: Some("a\0b".into()), ..Default::default() },
        Credential { client_id: Some("a\0b".into()), ..Default::default() },
    ] {
        assert!(matches!(login(&credential), Err(Error::InvalidArgument(_))));
    }
    assert!(matches!(logout("http://a\0b"), Err(Error::InvalidArgument(_))));
    assert!(matches!(install_license(Path::new("a\0b"), true), Err(Error::InvalidArgument(_))));
    assert!(matches!(
        Client::with_config(&ClientConfig::default().registries(vec![Registry::new("http://a\0b")])),
        Err(Error::InvalidArgument(_))
    ));
    assert!(matches!(
        Client::with_config(&ClientConfig::default().registries(vec![Registry::new(common::serve()).name("a\0b")])),
        Err(Error::InvalidArgument(_))
    ));
}

#[test]
fn explicit_registry_list_searches_every_entry() {
    let Some(_root) = isolated("explicit_registry_list_searches_every_entry") else { return };
    let cfg = ClientConfig::default()
        .registries(vec![Registry::new(common::serve()).name("first"), Registry::new(common::serve())])
        .credential_store(CredentialStore::None);
    let result = Client::with_config(&cfg).unwrap().search("test-driver-1").unwrap();
    assert_eq!(result.drivers.len(), 2, "{result:?}");
    assert!(result.drivers.iter().all(|d| d.path == "test-driver-1"));
    assert_eq!(result.warning, None);
}

#[test]
fn explicit_registry_options_are_validated() {
    let Some(_root) = isolated("explicit_registry_options_are_validated") else { return };
    let url = common::serve();
    let conflict = ClientConfig::default().base_url(&url).registries(vec![Registry::new(&url)]);
    assert!(matches!(Client::with_config(&conflict), Err(Error::Dbc(_))));
    let invalid = ClientConfig::default().registries(vec![Registry::new("http://\u{1}")]);
    assert!(matches!(Client::with_config(&invalid), Err(Error::Dbc(_))));
}

#[test]
fn default_registry_config_and_project_file_are_applied() {
    let Some(root) = isolated("default_registry_config_and_project_file_are_applied") else { return };
    let global = common::serve();
    write_global_config(&root, &format!("replace_defaults = true\n[[registries]]\nurl = '{global}'\n"));

    let result = Client::new().unwrap().search("test-driver-1").unwrap();
    assert_eq!(result.drivers.len(), 1, "{result:?}");
    assert_eq!(result.drivers[0].title, "Test Driver 1");
    assert_eq!(result.warning, None);

    let project = common::serve_with("drivers:\n  - path: test-driver-1\n    name: Project Driver\n".into(), None);
    let file = root.join("dbc.toml");
    fs::write(&file, format!("replace_defaults = true\n[[registries]]\nurl = '{project}'\n")).unwrap();
    let config = ClientConfig::default().registry_config(RegistryConfig::Default).project_file(&file);
    let result = Client::with_config(&config).unwrap().search("test-driver-1").unwrap();
    assert_eq!(result.drivers.iter().map(|d| d.title.as_str()).collect::<Vec<_>>(), ["Project Driver", "Test Driver 1"]);
    assert_eq!(result.warning, None);

    let result = Client::with_config(&config.registry_config(RegistryConfig::None))
        .unwrap()
        .search("test-driver-1")
        .unwrap();
    assert_eq!(result.drivers.len(), 1, "{result:?}");
    assert_eq!(result.drivers[0].title, "Project Driver");
    assert_eq!(result.warning, None);
}

#[test]
fn config_errors_fail_unless_explicit_registries_override_files() {
    let Some(root) = isolated("config_errors_fail_unless_explicit_registries_override_files") else { return };
    write_global_config(&root, "invalid = [");
    assert!(matches!(Client::new(), Err(Error::Dbc(_))));
    assert!(Client::with_config(&ClientConfig::default().registry_config(RegistryConfig::None)).is_ok());

    let missing = root.join("missing.toml");
    let config = ClientConfig::default().registry_config(RegistryConfig::None).project_file(&missing);
    assert!(matches!(Client::with_config(&config), Err(Error::Dbc(_))));
    let malformed = root.join("malformed.toml");
    fs::write(&malformed, "registries = [").unwrap();
    assert!(matches!(Client::with_config(&config.project_file(&malformed)), Err(Error::Dbc(_))));

    for explicit in [
        ClientConfig::default().base_url(common::serve()),
        ClientConfig::default().registries(vec![Registry::new(common::serve())]),
    ] {
        let client = Client::with_config(&explicit.project_file(&missing)).unwrap();
        let result = client.search("test-driver-1").unwrap();
        assert_eq!(result.drivers.len(), 1, "{result:?}");
        assert_eq!(result.warning, None);
    }
}

#[test]
fn login_logout_persist_and_remove_token_credentials() {
    let Some(root) = isolated("login_logout_persist_and_remove_token_credentials") else { return };
    assert_isolated_license_path(&root);
    let url = common::serve_with("drivers:\n  - path: private-driver\n".into(), Some("Bearer stored-token"));
    let mut credential = Credential {
        registry_url: url.clone(),
        auth_uri: format!("{url}/oauth/token"),
        token: Some("old-token".into()),
        ..Default::default()
    };
    login(&credential).unwrap();
    credential.token = Some("stored-token".into());
    credential.refresh_token = Some("refresh-value".into());
    credential.client_id = Some("client-value".into());
    login(&credential).unwrap();
    let store = root.join("data/dbc/credentials/credentials.toml");
    let saved = fs::read_to_string(&store).unwrap();
    for value in ["oauth", &url, &credential.auth_uri, "stored-token", "refresh-value", "client-value"] {
        assert!(saved.contains(value), "credential field {value:?} was not persisted");
    }
    assert!(!saved.contains("old-token"));

    let cfg = ClientConfig::default().base_url(&url);
    let result = Client::with_config(&cfg).unwrap().search("private-driver").unwrap();
    assert_eq!(result.drivers.len(), 1, "{result:?}");
    assert_eq!(result.warning, None);
    assert!(Client::with_config(&cfg.clone().credential_store(CredentialStore::None))
        .unwrap()
        .search("private-driver")
        .is_err());

    logout(&format!("{url}/another/path")).unwrap();
    assert!(!fs::read_to_string(store).unwrap().contains("stored-token"));
    assert!(Client::with_config(&cfg).unwrap().search("private-driver").is_err());
    match logout(&url) {
        Err(Error::Dbc(message)) => assert!(message.contains("no credentials found"), "{message}"),
        other => panic!("expected missing credentials error, got {other:?}"),
    }
}

#[test]
fn injected_credentials_override_their_host_and_fall_back_for_others() {
    let Some(root) = isolated("injected_credentials_override_their_host_and_fall_back_for_others") else { return };
    assert_isolated_license_path(&root);
    let url = common::serve_with("drivers:\n  - path: private-driver\n".into(), Some("Bearer stored-token"));
    let stored = Credential {
        registry_url: url.clone(),
        auth_uri: format!("{url}/oauth/token"),
        token: Some("stored-token".into()),
        ..Default::default()
    };
    login(&stored).unwrap();
    let unrelated = Credential {
        registry_url: "https://other.example.test".into(),
        auth_uri: "https://other.example.test/oauth/token".into(),
        token: Some("other-token".into()),
        ..Default::default()
    };
    let cfg = ClientConfig::default().base_url(&url).credential(unrelated);
    assert_eq!(Client::with_config(&cfg).unwrap().search("private-driver").unwrap().drivers.len(), 1);
    assert!(Client::with_config(&cfg.clone().credential_store(CredentialStore::None))
        .unwrap()
        .search("private-driver")
        .is_err());

    let stale = Credential { token: Some("stale-token".into()), ..stored.clone() };
    login(&stale).unwrap();
    for store in [CredentialStore::Default, CredentialStore::None] {
        let injected = cfg.clone().credential(stored.clone()).credential_store(store);
        let result = Client::with_config(&injected).unwrap().search("private-driver").unwrap();
        assert_eq!(result.drivers.len(), 1, "{result:?}");
        assert_eq!(result.warning, None);
    }
}

#[test]
fn login_logout_validate_urls_before_writing() {
    let Some(root) = isolated("login_logout_validate_urls_before_writing") else { return };
    assert_isolated_license_path(&root);
    for credential in [
        Credential { registry_url: "http://\u{1}".into(), ..Default::default() },
        Credential { registry_url: "https://example.test".into(), auth_uri: "http://\u{1}".into(), ..Default::default() },
    ] {
        assert!(matches!(login(&credential), Err(Error::Dbc(_))));
    }
    assert!(matches!(logout("http://\u{1}"), Err(Error::Dbc(_))));
    assert!(!root.join("data/dbc/credentials/credentials.toml").exists());
}

#[cfg(unix)]
#[test]
fn license_path_rejects_non_utf8() {
    use std::ffi::OsString;
    use std::os::unix::ffi::OsStringExt;

    // Handle this child before isolated() can replace its invalid data path.
    if std::env::var("DBC_RUST_TEST_CHILD").as_deref() == Ok("license_path_rejects_non_utf8_child") {
        let root = PathBuf::from(std::env::var_os("DBC_RUST_TEST_ROOT").unwrap());
        let data = PathBuf::from(std::env::var_os("XDG_DATA_HOME").unwrap());
        assert!(data.starts_with(&root));
        assert!(data.to_str().is_none());
        match license_path() {
            Err(Error::Dbc(message)) => assert!(message.contains("UTF-8"), "{message}"),
            other => panic!("expected Error::Dbc for a non-UTF-8 license path, got {other:?}"),
        }
        assert!(!data.join("dbc/credentials/columnar.lic").exists());
        assert!(!data.join("dbc/credentials/credentials.toml").exists());
        return;
    }

    let Some(root) = isolated("license_path_rejects_non_utf8") else { return };
    let data = root.join(OsString::from_vec(b"data-\xff".to_vec()));
    let output = Command::new(std::env::current_exe().unwrap())
        .args(["--exact", "license_path_rejects_non_utf8", "--nocapture"])
        .env("DBC_RUST_TEST_CHILD", "license_path_rejects_non_utf8_child")
        .env("XDG_DATA_HOME", data)
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "non-UTF-8 license path child failed:\n{}\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr),
    );
}

#[test]
fn install_license_requires_expected_filename_and_force_to_overwrite() {
    let Some(root) = isolated("install_license_requires_expected_filename_and_force_to_overwrite") else { return };
    let destination = assert_isolated_license_path(&root);
    assert!(!destination.exists());
    let source = root.join("columnar.lic");
    let alternate = root.join("renamed.lic");
    fs::write(&source, b"first license").unwrap();
    fs::write(&alternate, b"replacement license").unwrap();

    match install_license(&alternate, false) {
        Err(Error::Dbc(message)) => {
            assert!(message.contains("force"), "{message}");
            assert!(!message.contains("--force"), "{message}");
        }
        other => panic!("expected filename error, got {other:?}"),
    }
    assert!(!destination.exists());
    install_license(&source, false).unwrap();
    assert_eq!(fs::read(&destination).unwrap(), b"first license");
    fs::write(&source, b"not installed").unwrap();
    match install_license(&source, false) {
        Err(Error::Dbc(message)) => {
            assert!(message.contains("force"), "{message}");
            assert!(!message.contains("--force"), "{message}");
        }
        other => panic!("expected existing license error, got {other:?}"),
    }
    assert_eq!(fs::read(&destination).unwrap(), b"first license");
    install_license(&alternate, true).unwrap();
    assert_eq!(fs::read(&destination).unwrap(), b"replacement license");
    assert!(matches!(install_license(&root.join("missing.lic"), true), Err(Error::Dbc(_))));
    assert_eq!(fs::read(&destination).unwrap(), b"replacement license");
}
