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
//! Install/list/uninstall at the real user and system levels. These write to
//! machine-wide locations, so they only run when opted in with the variables
//! go.yml uses for the CLI's level suites: DBC_TEST_LEVEL_USER, and
//! DBC_TEST_LEVEL_SYSTEM (which needs elevated privileges).
mod common;

use columnar_dbc::{list_installed, uninstall, InstallLocation};
use std::path::Path;

fn installed(location: &InstallLocation) -> bool {
    list_installed(location).unwrap().iter().any(|d| d.id == "test-driver-1")
}

fn roundtrip(location: InstallLocation) {
    let manifest = common::client().install("test-driver-1", &location).unwrap();
    assert!(Path::new(&manifest.driver_path).exists(), "{manifest:?}");
    assert!(installed(&location));
    uninstall("test-driver-1", &location).unwrap();
    assert!(!installed(&location));
}

#[test]
fn user_level() {
    if std::env::var_os("DBC_TEST_LEVEL_USER").is_none() {
        eprintln!("skipped: set DBC_TEST_LEVEL_USER=1 to write to the real user level");
        return;
    }
    roundtrip(InstallLocation::User);
}

#[test]
fn system_level() {
    if std::env::var_os("DBC_TEST_LEVEL_SYSTEM").is_none() {
        eprintln!("skipped: set DBC_TEST_LEVEL_SYSTEM=1 to write to the real system level");
        return;
    }
    roundtrip(InstallLocation::System);
}
