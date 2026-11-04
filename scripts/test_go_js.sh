#!/usr/bin/env bash
# Copyright 2026 Columnar Technologies Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Run the Go tests of every module package the wasm build uses, under
# GOOS=js GOARCH=wasm in Node:
#   scripts/test_go_js.sh [build tags]
# With no tags this is the browser build; pass dbcnode for the Node build.
#
# Go's js runner (go_js_wasm_exec) hands the whole environment to the wasm
# runtime, whose argv+env cap a CI runner's environment exceeds, so the tests
# run with only what Go and Node need.
set -euo pipefail

tags="${1:-}"
cd "$(dirname "$0")/.."
gopath() { go env "$1"; }
env_args=(
  HOME="$HOME"
  PATH="$(gopath GOROOT)/lib/wasm:$(dirname "$(command -v node)"):$(dirname "$(command -v go)"):/usr/bin:/bin"
  GOCACHE="$(gopath GOCACHE)" GOPATH="$(gopath GOPATH)" GOMODCACHE="$(gopath GOMODCACHE)"
  GOTOOLCHAIN="$(gopath GOTOOLCHAIN)" GOFLAGS="$(gopath GOFLAGS)"
  GOOS=js GOARCH=wasm
)
module="$(go list -m)"
# Import paths contain no whitespace; word splitting also works on bash 3.2
# (macOS), which lacks mapfile.
packages="$(env -i "${env_args[@]}" go list -tags "$tags" -deps \
  -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./wasm | grep -E "^${module}(/|$)")"
# shellcheck disable=SC2086
env -i "${env_args[@]}" go test -count=1 -timeout 5m -tags "$tags" $packages
