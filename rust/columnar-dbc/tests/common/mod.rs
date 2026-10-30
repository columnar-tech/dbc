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

/// A request as seen by an [`http_serve`] handler.
pub struct Request {
    /// The path, without the query string dbc may append.
    pub path: String,
}

/// Serves GET requests on 127.0.0.1 until the test process exits, answering
/// each with `handler`'s (status, body) and `Connection: close`. Each
/// connection gets its own thread, so a client dropping a connection, as Go's
/// HTTP transport does with surplus ones, ends only that thread.
pub fn http_serve(handler: impl Fn(&Request) -> (u16, Vec<u8>) + Send + Sync + 'static) -> String {
    use std::io::{BufRead, BufReader, Write};
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let base = format!("http://{}", listener.local_addr().unwrap());
    let handler = std::sync::Arc::new(handler);
    std::thread::spawn(move || {
        for stream in listener.incoming().flatten() {
            let handler = handler.clone();
            std::thread::spawn(move || {
                let mut reader = BufReader::new(&stream);
                let mut line = String::new();
                if reader.read_line(&mut line).is_err() {
                    return;
                }
                let target = line.split_whitespace().nth(1).unwrap_or_default();
                let path = target.split('?').next().unwrap_or_default().to_string();
                // Skip the headers; requests carry no body.
                loop {
                    let mut header = String::new();
                    match reader.read_line(&mut header) {
                        Ok(0) | Err(_) => return,
                        Ok(_) if header.trim_end().is_empty() => break,
                        Ok(_) => {}
                    }
                }
                let (status, body) = handler(&Request { path });
                let head = format!("HTTP/1.1 {status} \r\nContent-Length: {}\r\nConnection: close\r\n\r\n", body.len());
                let mut stream = &stream;
                let _ = stream.write_all(head.as_bytes()).and_then(|()| stream.write_all(&body));
            });
        }
    });
    base
}

/// Serves the fixture registry until the test process exits.
pub fn serve() -> String {
    let (index, valid, tampered) = (index(), fixture("test-driver-1.tar.gz"), fixture("test-driver-tampered.tar.gz"));
    http_serve(move |req| {
        if req.path.starts_with("/index.yaml") {
            (200, index.as_bytes().to_vec())
        } else if req.path.starts_with("/test-driver-tampered/") {
            (200, tampered.clone())
        } else if req.path.ends_with(".tar.gz") {
            (200, valid.clone())
        } else {
            (404, b"not found".to_vec())
        }
    })
}

/// A client for a fresh fixture registry.
pub fn client() -> Client {
    Client::with_config(&ClientConfig { base_url: Some(serve()), ..Default::default() }).unwrap()
}
