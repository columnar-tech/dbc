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
//! `Client::install` without a location follows ADBC_DRIVER_PATH, like
//! `dbc install` without -l. dbc reads the environment when the process
//! starts, so the test re-runs itself with the variable set.
mod common;

use columnar_dbc::{list_installed, InstallLocation};
use std::path::Path;
use std::process::Command;

const CHILD: &str = "COLUMNAR_DBC_TEST_DEFAULT_CHILD";

#[test]
fn install_defaults_to_adbc_driver_path() {
    if std::env::var_os(CHILD).is_some() {
        let dir = std::env::var_os("ADBC_DRIVER_PATH").unwrap();
        let manifest = common::client().install("test-driver-1").unwrap();
        assert!(Path::new(&manifest.driver_path).starts_with(&dir), "{manifest:?}");
        let installed = list_installed(&InstallLocation::Default).unwrap();
        assert_eq!(installed.len(), 1, "{installed:?}");
        assert_eq!(list_installed(&InstallLocation::Directory(dir.into())).unwrap().len(), 1);
        return;
    }

    let dir = tempfile::tempdir().unwrap();
    let status = Command::new(std::env::current_exe().unwrap())
        .args(["--exact", "install_defaults_to_adbc_driver_path", "--nocapture"])
        .env(CHILD, "1")
        .env("ADBC_DRIVER_PATH", dir.path())
        .env_remove("VIRTUAL_ENV")
        .env_remove("CONDA_PREFIX")
        .status()
        .unwrap();
    assert!(status.success());
}
