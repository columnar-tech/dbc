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

//! Carries the prebuilt dbc static library and its link flags for one
//! platform. Depend on `columnar-dbc` instead of this crate.
#![no_std]

/// Starts the Go runtime under the MSVC CRT.
///
/// A Go c-archive registers its runtime entry point in `.ctors`. The MinGW CRT
/// runs that section, but the MSVC CRT does not. Without this, the first dbc
/// call blocks forever in `_cgo_wait_runtime_init_done`. `.CRT$XCU` is the
/// MSVC CRT's C++ static-initializer table. rustc emits an undefined reference
/// to each `#[used]` static, which forces this object out of the rlib and into
/// the link.
#[cfg(target_env = "msvc")]
mod msvc_runtime_init {
    extern "C" {
        fn _rt0_amd64_windows_lib();
    }

    #[used]
    #[link_section = ".CRT$XCU"]
    static START_GO_RUNTIME: unsafe extern "C" fn() = _rt0_amd64_windows_lib;
}
