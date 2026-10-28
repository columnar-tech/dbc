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
use std::{env, path::PathBuf};

fn main() {
    // The archive is built with a MinGW toolchain and references the MinGW
    // CRT, so it cannot be linked by MSVC.
    assert_eq!(
        env::var("CARGO_CFG_TARGET_ENV").unwrap_or_default(),
        "gnu",
        "columnar-dbc-sys-windows-x64 only supports x86_64-pc-windows-gnu"
    );
    let native = PathBuf::from(env::var("CARGO_MANIFEST_DIR").unwrap()).join("native");
    let archive = native.join("libdbc.a");
    println!("cargo:rerun-if-changed={}", archive.display());
    assert!(
        archive.exists(),
        "{} is missing; build it with: CGO_ENABLED=1 go build -tags netgo,osusergo -buildmode=c-archive -o {}/libdbc.a ./capi",
        archive.display(),
        native.display()
    );
    println!("cargo:rustc-link-search=native={}", native.display());
    println!("cargo:rustc-link-lib=static:+verbatim=libdbc.a");
    // Emitted after the archive so the linker resolves its imports: the Go
    // runtime imports kernel32 directly, and cgo needs the MinGW CRT.
    for lib in ["kernel32", "mingwex", "mingw32", "msvcrt"] {
        println!("cargo:rustc-link-lib=dylib={lib}");
    }
}
