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

"use strict";

// Add a new entry here to support a new platform across both the
// build script (create_packages.js) and the wrapper shim (bin/dbc.js).
const PLATFORMS = [
  {
    goosArch: "darwin-arm64",
    npmPkg: "@columnar-tech/dbc-darwin-arm64",
    binary: "dbc",
    os: "darwin",
    cpu: "arm64",
  },
  {
    goosArch: "darwin-amd64",
    npmPkg: "@columnar-tech/dbc-darwin-x64",
    binary: "dbc",
    os: "darwin",
    cpu: "x64",
  },
  {
    goosArch: "linux-arm64",
    npmPkg: "@columnar-tech/dbc-linux-arm64",
    binary: "dbc",
    os: "linux",
    cpu: "arm64",
  },
  {
    goosArch: "linux-amd64",
    npmPkg: "@columnar-tech/dbc-linux-x64",
    binary: "dbc",
    os: "linux",
    cpu: "x64",
  },
  {
    goosArch: "windows-amd64",
    npmPkg: "@columnar-tech/dbc-win32-x64",
    binary: "dbc.exe",
    os: "win32",
    cpu: "x64",
  },
];

module.exports = { PLATFORMS };
