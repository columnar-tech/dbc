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
  python3 scripts/stage_capi.py <archive> <goos> <goarch> <version>

Fails if the gzipped archive would push the platform crate past crates.io's
10 MiB limit. Copies the archive to rust/platforms/<platform>/native/libdbc.a
(GoReleaser names the Windows one libdbc.lib) and the cgo header next to it.

Rewrites macOS archives in place in Darwin ar format first: Go builds them on
Linux with GNU ar, whose members are not 8-byte aligned, and Apple's arm64
linker rejects that ("64-bit mach-o member ... not 8-byte aligned"). The
GoReleaser libdbc-dev archive then picks up the rewritten file.

For macOS and Windows, also writes dist/devel/<goos>_<goarch>/dbc.pc for the
libdbc-dev archive. It is relocatable: the archive keeps the library, header,
and dbc.pc in one directory, so set PKG_CONFIG_PATH to wherever it is unpacked.
"""

import gzip
import pathlib
import shutil
import subprocess
import sys
import tempfile

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
# Libs.private for the static library; Linux packages use stage_devel.py.
ARCHIVE_PRIVATE_LIBS = {
    # crypto/x509 resolves system roots through the Security framework.
    "darwin": "-framework CoreFoundation -framework Security",
    "windows": "",
}


def repack_darwin(archive: pathlib.Path) -> None:
    """Rewrite archive in Darwin format with Zig's llvm-ar, keeping member order."""
    with tempfile.TemporaryDirectory() as tmp:
        members = subprocess.run(["zig", "ar", "t", str(archive.resolve())], check=True,
                                 capture_output=True, text=True).stdout.split()
        subprocess.run(["zig", "ar", "x", str(archive.resolve())], cwd=tmp, check=True)
        repacked = pathlib.Path(tmp) / "repacked.a"
        subprocess.run(["zig", "ar", "--format=darwin", "rcsD", str(repacked), *members],
                       cwd=tmp, check=True)
        shutil.copyfile(repacked, archive)


def main() -> int:
    if len(sys.argv) != 5:
        print(__doc__, file=sys.stderr)
        return 2
    archive = pathlib.Path(sys.argv[1])
    platform = PLATFORMS.get((sys.argv[2], sys.argv[3]))
    if platform is None:
        print(f"error: no Rust platform crate for {sys.argv[2]}/{sys.argv[3]}", file=sys.stderr)
        return 1

    if sys.argv[2] == "darwin":
        repack_darwin(archive)

    # The linker leaves the archive and header executable; the libdbc-dev
    # archives and the Rust platform crates should ship them 0644. (The Linux
    # packages can't: GoReleaser packages build artifacts with mode 0755.)
    archive.chmod(0o644)
    archive.with_suffix(".h").chmod(0o644)

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

    goos, goarch, version = sys.argv[2], sys.argv[3], sys.argv[4]
    if goos in ARCHIVE_PRIVATE_LIBS:
        devel = REPO / "dist" / "devel" / f"{goos}_{goarch}"
        devel.mkdir(parents=True, exist_ok=True)
        (devel / "dbc.pc").write_text(
            "prefix=${pcfiledir}\n"
            "libdir=${prefix}\n"
            "includedir=${prefix}\n"
            "\n"
            "Name: dbc\n"
            "Description: C API of dbc, the ADBC driver installer\n"
            "URL: https://columnar.tech/dbc\n"
            f"Version: {version}\n"
            f"Libs: -L${{libdir}} -l{'dbc' if goos == 'darwin' else ':' + archive.name}\n"
            f"Libs.private: {ARCHIVE_PRIVATE_LIBS[goos]}\n"
            "Cflags: -I${includedir}\n",
            encoding="utf-8",
        )
        print(f"wrote {devel / 'dbc.pc'}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
