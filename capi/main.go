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

//go:build cgo

// Command capi is dbc's C API, built only as a static library:
//
//	CGO_ENABLED=1 go build -tags netgo,osusergo -buildmode=c-archive -o <dir>/libdbc.a ./capi
//
// cgo generates <dir>/libdbc.h from the preamble and //export functions in
// exports.go; that header is the API's only declaration. It backs the
// columnar-dbc Rust crates, whose bindings bindgen generates from the header.
package main

// main is required by -buildmode=c-archive and never runs.
func main() {}
