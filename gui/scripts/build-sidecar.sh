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

set -euo pipefail

# Build the dbc sidecar binary for a specific Tauri target triple.
# Usage: ./build-sidecar.sh [--target <rustc-triple>]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BINARIES_DIR="$SCRIPT_DIR/../src-tauri/binaries"

TARGET="${TAURI_ENV_TARGET_TRIPLE:-}"
while [[ $# -gt 0 ]]; do
  case $1 in
    --target) TARGET="$2"; shift 2 ;;
    *) echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

if [[ -z "$TARGET" ]]; then
  # Default to host triple
  TARGET="$(rustc -vV 2>/dev/null | grep 'host:' | awk '{print $2}')"
  if [[ -z "$TARGET" ]]; then
    echo "Error: could not determine host triple. Install Rust or pass --target." >&2
    exit 1
  fi
fi

# Map rustc triple → Go GOOS/GOARCH
case "$TARGET" in
  x86_64-apple-darwin)        GOOS=darwin  GOARCH=amd64 EXT="" ;;
  aarch64-apple-darwin)       GOOS=darwin  GOARCH=arm64 EXT="" ;;
  x86_64-pc-windows-msvc)     GOOS=windows GOARCH=amd64 EXT=".exe" ;;
  x86_64-unknown-linux-gnu)   GOOS=linux   GOARCH=amd64 EXT="" ;;
  aarch64-unknown-linux-gnu)  GOOS=linux   GOARCH=arm64 EXT="" ;;
  *)
    echo "Error: unsupported target triple: $TARGET" >&2
    exit 1
    ;;
esac

OUTPUT="$BINARIES_DIR/dbc-${TARGET}${EXT}"
mkdir -p "$BINARIES_DIR"

echo "Building dbc sidecar for $TARGET (GOOS=$GOOS GOARCH=$GOARCH)..."

if ! command -v go &>/dev/null; then
  echo "Error: go command not found. Install Go first." >&2
  exit 1
fi

VERSION="${VERSION:-dev}"
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build \
  -trimpath \
  -ldflags="-s -w -X main.version=${VERSION}" \
  -o "$OUTPUT" \
  "$REPO_ROOT/cmd/dbc"

echo "Built: $OUTPUT"
echo "SHA256: $(shasum -a 256 "$OUTPUT" | awk '{print $1}')"
