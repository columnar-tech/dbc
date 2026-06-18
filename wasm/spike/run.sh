#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
out="${TMPDIR:-/tmp}/dbc-spike"
mkdir -p "$out"
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$out/wasm_exec.js"
( cd "$root" && GOWORK=off GOOS=js GOARCH=wasm go build -tags dbcnode -o "$out/dbc.wasm" ./wasm )
export WASM_EXEC="$out/wasm_exec.js" WORKTREE="$root" WASM="$out/dbc.wasm"
echo "### core harness"
node "$here/harness.cjs"
echo "### auth harness"
node "$here/auth_harness.cjs"
