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
//! `dbc install` without -l, as it is when the call is made: changes after
//! libdbc has started still apply. This file holds a single test because it
//! changes the process environment.
mod common;

use columnar_dbc::{list_installed, InstallLocation};
use std::env;
use std::path::Path;

#[test]
fn install_follows_adbc_driver_path_set_after_startup() {
    env::remove_var("VIRTUAL_ENV");
    env::remove_var("CONDA_PREFIX");
    env::remove_var("ADBC_DRIVER_PATH");
    let client = common::client(); // libdbc and its Go runtime are running now

    let dir = tempfile::tempdir().unwrap();
    env::set_var("ADBC_DRIVER_PATH", dir.path());
    let manifest = client.install("test-driver-1").unwrap();
    assert!(Path::new(&manifest.driver_path).starts_with(dir.path()), "{manifest:?}");
    assert_eq!(list_installed(&InstallLocation::Default).unwrap().len(), 1);

    // Unsetting it applies too: Default is the user level again.
    env::remove_var("ADBC_DRIVER_PATH");
    let default = list_installed(&InstallLocation::Default).unwrap();
    assert!(
        default.iter().all(|d| !Path::new(&d.file_path).starts_with(dir.path())),
        "{default:?}"
    );
}
