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

const assert = require("assert");
const { normalizeLocation } = require("../index.cjs");

const cases = [
  ["/tmp/drivers", "/tmp/drivers"],
  ["/etc/adbc/drivers", "/etc/adbc/drivers"],
  ["C:\\drivers", "C:/drivers"],
  ["C:/drivers", "C:/drivers"],
  ["C:\\a\\b\\c", "C:/a/b/c"],
  ["C:drivers", "C:/drivers"],
  ["C:", "C:/"],
  ["d:\\Lower", "d:/Lower"],
];

for (const [input, want] of cases) {
  assert.strictEqual(normalizeLocation(input), want, `normalizeLocation(${JSON.stringify(input)})`);
}

console.log(`normalizeLocation: ${cases.length} cases passed`);
