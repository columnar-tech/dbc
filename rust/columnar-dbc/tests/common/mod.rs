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
use columnar_dbc::{Client, ClientConfig};
use std::fs;
use std::path::Path;

const PLATFORMS: [&str; 5] = ["linux_amd64", "linux_arm64", "macos_amd64", "macos_arm64", "windows_amd64"];

/// A registry index with every CI platform for `test-driver-1` (validly
/// signed) and `test-driver-tampered` (library modified after signing).
fn index() -> String {
    let mut index = String::from("drivers:\n");
    for (path, title) in [("test-driver-1", "Test Driver 1"), ("test-driver-tampered", "Tampered Driver")] {
        index += &format!(
            "  - name: {title}\n    description: Test driver\n    license: MIT\n    path: {path}\n    pkginfo:\n      - version: v1.0.0\n        packages:\n"
        );
        for platform in PLATFORMS {
            index += &format!("          - platform: {platform}\n            url: {path}/1.0.0/{platform}.tar.gz\n");
        }
    }
    index
}

fn fixture(name: &str) -> Vec<u8> {
    fs::read(Path::new(env!("CARGO_MANIFEST_DIR")).join("../../cmd/dbc/testdata").join(name)).unwrap()
}

/// Serves the fixture registry until the test process exits.
pub fn serve() -> String {
    let (index, valid, tampered) = (index(), fixture("test-driver-1.tar.gz"), fixture("test-driver-tampered.tar.gz"));
    let server = tiny_http::Server::http("127.0.0.1:0").unwrap();
    let base = format!("http://{}", server.server_addr().to_ip().unwrap());
    std::thread::spawn(move || {
        for req in server.incoming_requests() {
            // dbc may append query parameters; route on the path alone.
            let url = req.url().split('?').next().unwrap_or_default().to_string();
            let body = if url.starts_with("/index.yaml") {
                Some(index.as_bytes().to_vec())
            } else if url.starts_with("/test-driver-tampered/") {
                Some(tampered.clone())
            } else if url.ends_with(".tar.gz") {
                Some(valid.clone())
            } else {
                None
            };
            let resp = match body {
                Some(body) => tiny_http::Response::from_data(body),
                None => tiny_http::Response::from_data(b"not found".to_vec()).with_status_code(404),
            };
            let _ = req.respond(resp);
        }
    });
    base
}

/// A client for a fresh fixture registry.
pub fn client() -> Client {
    Client::with_config(&ClientConfig { base_url: Some(serve()), ..Default::default() }).unwrap()
}
