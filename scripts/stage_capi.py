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

"""Check a GoReleaser-built capi archive and stage it into its Rust platform crate.

Run by the post hook of the `capi` build in .goreleaser.yaml:
  python3 scripts/stage_capi.py <archive> <goos> <goarch>

Fails if the gzipped archive would push the platform crate past crates.io's
10 MiB limit. Copies the archive to rust/platforms/<platform>/native/libdbc.a
(GoReleaser names the Windows one libdbc.lib) and the cgo header next to it.
"""

import gzip
import pathlib
import shutil
import sys

REPO = pathlib.Path(__file__).resolve().parent.parent
# crates.io rejects .crate files over 10 MiB; keep headroom for the rest of the crate.
MAX_COMPRESSED = 9 * 1024 * 1024
PLATFORMS = {
    ("linux", "amd64"): "linux-x64",
    ("linux", "arm64"): "linux-arm64",
    ("darwin", "amd64"): "darwin-x64",
    ("darwin", "arm64"): "darwin-arm64",
    ("windows", "amd64"): "windows-x64",
}


def main() -> int:
    if len(sys.argv) != 4:
        print(__doc__, file=sys.stderr)
        return 2
    archive = pathlib.Path(sys.argv[1])
    platform = PLATFORMS.get((sys.argv[2], sys.argv[3]))
    if platform is None:
        print(f"error: no Rust platform crate for {sys.argv[2]}/{sys.argv[3]}", file=sys.stderr)
        return 1

    raw = archive.read_bytes()
    compressed = len(gzip.compress(raw, compresslevel=9))
    print(f"{archive}: {len(raw):,} bytes, {compressed:,} gzipped")
    if compressed > MAX_COMPRESSED:
        print(f"error: gzipped archive exceeds {MAX_COMPRESSED:,} bytes; "
              "crates.io would reject the platform crate", file=sys.stderr)
        return 1

    native = REPO / "rust" / "platforms" / platform / "native"
    native.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(archive, native / "libdbc.a")
    shutil.copyfile(archive.with_suffix(".h"), native / "libdbc.h")
    print(f"staged into {native}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
