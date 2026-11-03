#!/usr/bin/env python3
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

"""Package or publish the columnar-dbc crates with the archives staged by stage_capi.py.

Run by the global `after` hooks in .goreleaser.yaml:
  python3 scripts/publish_crates.py --version 0.4.1-snapshot --dry-run   # snapshots: cargo package
  python3 scripts/publish_crates.py --version 0.4.0                      # releases: cargo publish

Stamps --version over the 0.0.0-dev placeholder in rust/Cargo.toml for the
duration of the cargo command, then restores rust/Cargo.toml and Cargo.lock.
Publishing skips crates whose version is already on crates.io, so re-running a
partially published release finishes the remaining crates.
"""

import argparse
import pathlib
import subprocess
import sys
import urllib.error
import urllib.request

REPO = pathlib.Path(__file__).resolve().parent.parent
RUST = REPO / "rust"
MANIFEST = RUST / "Cargo.toml"
LOCK = RUST / "Cargo.lock"
PLACEHOLDER = "0.0.0-dev"
PLATFORMS = ("linux-x64", "linux-arm64", "darwin-x64", "darwin-arm64", "windows-x64")
CRATES = ("columnar-dbc", "columnar-dbc-sys", *(f"columnar-dbc-sys-{p}" for p in PLATFORMS))


def published(crate: str, version: str) -> bool:
    request = urllib.request.Request(
        f"https://crates.io/api/v1/crates/{crate}/{version}",
        headers={"User-Agent": "dbc-release (https://github.com/columnar-tech/dbc)"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30):
            return True
    except urllib.error.HTTPError as err:
        if err.code == 404:
            return False
        raise


def main() -> int:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument("--version", required=True, help="crate version, e.g. 0.4.0")
    parser.add_argument("--dry-run", action="store_true", help="cargo package instead of publish")
    args = parser.parse_args()

    missing = [p for p in PLATFORMS if not (RUST / "platforms" / p / "native" / "libdbc.a").is_file()]
    if missing:
        print(f"error: no staged archive for {', '.join(missing)}; "
              "the GoReleaser capi build stages them", file=sys.stderr)
        return 1

    manifest, lock = MANIFEST.read_text(), LOCK.read_text()
    if PLACEHOLDER not in manifest:
        print(f"error: {PLACEHOLDER} not found in {MANIFEST}", file=sys.stderr)
        return 1
    version = args.version.removeprefix("v")
    done = [] if args.dry_run else [c for c in CRATES if published(c, version)]
    if len(done) == len(CRATES):
        print(f"all crates already published at {version}")
        return 0
    for crate in done:
        print(f"{crate} {version} already published; skipping")

    MANIFEST.write_text(manifest.replace(PLACEHOLDER, version), newline="\n")
    try:
        # --no-verify: platform crates hold foreign-target archives that can't
        # be compiled on this host; the rust_test CI jobs cover every target.
        cmd = ["cargo", "package" if args.dry_run else "publish",
               "--manifest-path", str(MANIFEST), "--workspace", "--no-verify", "--allow-dirty"]
        for crate in done:
            cmd += ["--exclude", crate]
        subprocess.run(cmd, check=True)
    finally:
        MANIFEST.write_text(manifest, newline="\n")
        LOCK.write_text(lock, newline="\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
