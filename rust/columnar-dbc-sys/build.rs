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
use std::{env, path::Path};

fn main() {
    for name in ["DBC_SYS_LIB_DIR", "DBC_SYS_USE_PKG_CONFIG", "DBC_DYNAMIC"] {
        println!("cargo:rerun-if-env-changed={name}");
    }
    println!("cargo:rustc-check-cfg=cfg(dbc_bundled)");
    println!("cargo:rustc-check-cfg=cfg(dbc_msvc_go_init)");

    let mut is_static = env::var_os("DBC_DYNAMIC").is_none();
    if let Some(dir) = env::var_os("DBC_SYS_LIB_DIR") {
        link_custom(Path::new(&dir), is_static);
    } else if env::var_os("DBC_SYS_USE_PKG_CONFIG").is_none()
        && env::var_os("CARGO_FEATURE_BUNDLED").is_some()
    {
        link_bundled();
        is_static = true;
    } else {
        link_pkg_config(is_static);
    }

    if is_static && env::var("CARGO_CFG_TARGET_ENV").as_deref() == Ok("msvc") {
        assert!(
            env::var("CARGO_CFG_TARGET_ARCH").as_deref() == Ok("x86_64"),
            "columnar-dbc-sys: only x86_64 MSVC static links are supported"
        );
        println!("cargo:rustc-cfg=dbc_msvc_go_init");
    }
}

fn link_custom(dir: &Path, is_static: bool) {
    if is_static {
        let archive = dir.join("libdbc.a");
        println!("cargo:rerun-if-changed={}", archive.display());
        assert!(archive.is_file(), "columnar-dbc-sys: {} is missing", archive.display());
    }
    println!("cargo:rustc-link-search=native={}", dir.display());
    if is_static {
        println!("cargo:rustc-link-lib=static:+verbatim=libdbc.a");
        match env::var("CARGO_CFG_TARGET_OS").as_deref() {
            Ok("linux") => println!("cargo:rustc-link-lib=dylib=pthread"),
            Ok("macos") => {
                println!("cargo:rustc-link-lib=framework=CoreFoundation");
                println!("cargo:rustc-link-lib=framework=Security");
            }
            _ => {}
        }
    } else {
        println!("cargo:rustc-link-lib=dylib=dbc");
    }
}

/// The libdbc versions these bindings can use: the release that matches the
/// crate version, up to (excluding) the next minor release before 1.0, or the
/// next major release after. The C ABI (option keys, target and field ids) can
/// change between those, so an older or newer libdbc would compile and then
/// fail or misbehave at run time. `None` for the unreleased 0.0.0-dev
/// placeholder used in the repository.
fn libdbc_version_range() -> Option<(String, String)> {
    let version = env::var("CARGO_PKG_VERSION").unwrap();
    if version == "0.0.0-dev" {
        return None;
    }
    let release = version.split(['-', '+']).next().unwrap();
    let mut parts = release.split('.').map(|p| p.parse::<u64>().unwrap());
    let (major, minor) = (parts.next().unwrap(), parts.next().unwrap());
    let upper = if major == 0 { format!("0.{}.0", minor + 1) } else { format!("{}.0.0", major + 1) };
    Some((release.to_string(), upper))
}

fn link_pkg_config(is_static: bool) {
    let range = libdbc_version_range();
    let mut config = pkg_config::Config::new();
    config.cargo_metadata(false).env_metadata(true).statik(is_static);
    if let Some((lower, upper)) = &range {
        config.range_version(lower.as_str()..upper.as_str());
    }
    let library = config.probe("dbc").unwrap_or_else(|err| {
        let required = range
            .as_ref()
            .map(|(lower, upper)| format!(" (libdbc >= {lower}, < {upper}, matching this crate's version)"))
            .unwrap_or_default();
        panic!(
            "columnar-dbc-sys: {err}\n\nInstall libdbc-dev/libdbc-devel{required} and pkg-config, set \
             DBC_SYS_LIB_DIR, or enable the `bundled` feature to link a prebuilt libdbc."
        )
    });
    let libdir = pkg_config::get_variable("dbc", "libdir")
        .unwrap_or_else(|err| panic!("columnar-dbc-sys: {err}"));
    for path in &library.link_paths {
        println!("cargo:rustc-link-search=native={}", path.display());
    }
    // pkg-config strips system search paths from --libs, but Rust still needs
    // them when linking libdbc statically.
    println!("cargo:rustc-link-search=native={libdir}");
    if is_static {
        // pkg-config-rs otherwise links archives under system roots dynamically,
        // even with statik(true). Only libdbc itself must be linked statically.
        println!("cargo:rustc-link-lib=static=dbc");
        for lib in &library.libs {
            if lib != "dbc" {
                println!("cargo:rustc-link-lib=dylib={lib}");
            }
        }
        for framework in &library.frameworks {
            println!("cargo:rustc-link-lib=framework={framework}");
        }
    } else {
        println!("cargo:rustc-link-lib=dylib=dbc");
    }
}

fn link_bundled() {
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
    println!("cargo:rustc-cfg=dbc_bundled");
}
