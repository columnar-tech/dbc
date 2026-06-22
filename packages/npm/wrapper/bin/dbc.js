#!/usr/bin/env node
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

const { execFileSync } = require("child_process");
const { platform, arch } = process;
const { PLATFORMS } = require("../platforms.js");

const entry = PLATFORMS.find((p) => p.os === platform && p.cpu === arch);

if (!entry) {
  console.error(
    `dbc: unsupported platform: ${platform}/${arch}. Please file an issue at https://github.com/columnar-tech/dbc/issues`,
  );
  process.exit(1);
}

let binPath;
try {
  binPath = require.resolve(`${entry.npmPkg}/bin/${entry.binary}`);
} catch {
  console.error(
    `dbc: could not find the platform package ${entry.npmPkg}.\n` +
      `  Try reinstalling: npm install -g @columnar-tech/dbc`,
  );
  process.exit(1);
}

try {
  execFileSync(binPath, process.argv.slice(2), { stdio: "inherit" });
} catch (err) {
  process.exit(err.status ?? 1);
}
