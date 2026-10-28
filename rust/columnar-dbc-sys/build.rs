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
use std::env;

fn main() {
    let os = env::var("CARGO_CFG_TARGET_OS").unwrap_or_default();
    let arch = env::var("CARGO_CFG_TARGET_ARCH").unwrap_or_default();
    let target_env = env::var("CARGO_CFG_TARGET_ENV").unwrap_or_default();
    let target = env::var("TARGET").unwrap_or_default();
    let supported = match os.as_str() {
        "linux" => matches!((arch.as_str(), target_env.as_str()), ("x86_64" | "aarch64", "gnu")),
        "macos" => matches!(arch.as_str(), "x86_64" | "aarch64"),
        // Only the triples CI links and tests; e.g. -gnullvm and -uwp-* are not.
        "windows" => matches!(target.as_str(), "x86_64-pc-windows-msvc" | "x86_64-pc-windows-gnu"),
        _ => false,
    };
    if !supported {
        panic!(
            "columnar-dbc-sys: no prebuilt dbc library for target `{target}`. Supported: \
             x86_64/aarch64 Linux (glibc), x86_64/aarch64 macOS, \
             x86_64-pc-windows-msvc, x86_64-pc-windows-gnu."
        );
    }
}
