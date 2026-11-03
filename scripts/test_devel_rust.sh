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

# Tests the columnar-dbc crate against an installed libdbc-dev/libdbc-devel
# package with the default (pkg-config) build, static and dynamic, and with
# DBC_SYS_LIB_DIR. Checks each roundtrip test executable's libdbc linkage.
#
# Usage: scripts/test_devel_rust.sh <build-root>
set -euo pipefail

build_root=$1
manifest="$(cd "$(dirname "$0")/.." && pwd)/rust/Cargo.toml"

for mode in pkg-config-static pkg-config-dynamic custom-static; do
  unset DBC_DYNAMIC DBC_SYS_LIB_DIR DBC_SYS_USE_PKG_CONFIG
  case "$mode" in
    pkg-config-static) ;;
    pkg-config-dynamic) export DBC_DYNAMIC=1 ;;
    custom-static)
      DBC_SYS_LIB_DIR=$(pkg-config --variable=libdir dbc)
      export DBC_SYS_LIB_DIR
      ;;
  esac
  # Separate, fresh target dirs prevent stale binaries from passing the
  # linkage assertions. No bundled archives are staged for these builds.
  cargo test --manifest-path "$manifest" -p columnar-dbc \
    --target-dir "$build_root/$mode" --message-format=json \
    | tee "$build_root/$mode.log"
  executable=$(python3 - "$build_root/$mode.log" <<'PY'
import json
import sys

executables = set()
with open(sys.argv[1]) as log:
    for line in log:
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue  # The test harness also writes non-JSON output.
        if (message.get("reason") == "compiler-artifact"
                and message["target"]["name"] == "roundtrip"
                and message["profile"]["test"]
                and message.get("executable")):
            executables.add(message["executable"])
if len(executables) != 1:
    sys.exit(f"Expected one roundtrip test executable, got {executables}")
print(executables.pop())
PY
  )
  test -x "$executable"
  linkage=$(ldd "$executable")
  printf '%s: %s\n%s\n' "$mode" "$executable" "$linkage"
  if grep -Fq 'not found' <<< "$linkage"; then
    echo "Unresolved runtime dependency" >&2
    exit 1
  fi
  if [ "$mode" = pkg-config-dynamic ]; then
    grep -Fq 'libdbc.so' <<< "$linkage"
  elif grep -Fq 'libdbc.so' <<< "$linkage"; then
    echo "Static mode linked libdbc.so" >&2
    exit 1
  fi
done
