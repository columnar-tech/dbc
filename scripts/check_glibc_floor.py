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

"""Fail if a Linux libdbc needs a newer glibc than the documented floor.

Run by the post hooks of the `capi` and `capi-shared` builds in
.goreleaser.yaml, before anything is packaged or published:
  python3 scripts/check_glibc_floor.py <libdbc.a|libdbc.so> <goarch>

A shared library records the glibc symbol versions it needs; they are read
with readelf. A static archive records none: the program it is linked into
binds them. It is linked whole, with Zig, against the floor's glibc stubs,
which fails if any object needs a symbol the floor lacks, and the resulting
program's versions are checked the same way.
"""

import pathlib
import re
import subprocess
import sys
import tempfile

FLOOR = (2, 28)
ZIG_ARCH = {"amd64": "x86_64", "arm64": "aarch64"}


def required_versions(elf: pathlib.Path) -> set[tuple[int, ...]]:
    out = subprocess.run(
        ["readelf", "--version-info", "--wide", str(elf)],
        check=True, capture_output=True, text=True,
    ).stdout
    return {tuple(map(int, v.split("."))) for v in re.findall(r"\bGLIBC_(\d+(?:\.\d+)+)\b", out)}


def link_archive(archive: pathlib.Path, goarch: str, out: pathlib.Path) -> None:
    main = out.with_suffix(".c")
    main.write_text("int main(void) { return 0; }\n")
    target = f"{ZIG_ARCH[goarch]}-linux-gnu.{FLOOR[0]}.{FLOOR[1]}"
    subprocess.run(
        ["zig", "cc", "-target", target, str(main),
         "-Wl,--whole-archive", str(archive), "-Wl,--no-whole-archive",
         "-o", str(out)],
        check=True,
    )


def main() -> int:
    if len(sys.argv) != 3 or sys.argv[2] not in ZIG_ARCH:
        print(__doc__, file=sys.stderr)
        return 2
    lib, goarch = pathlib.Path(sys.argv[1]), sys.argv[2]
    floor = ".".join(map(str, FLOOR))
    with tempfile.TemporaryDirectory() as tmp:
        elf = lib
        if lib.suffix == ".a":
            elf = pathlib.Path(tmp, "linked")
            try:
                link_archive(lib, goarch, elf)
            except subprocess.CalledProcessError:
                print(f"{lib}: does not link against glibc {floor}", file=sys.stderr)
                return 1
        versions = required_versions(elf)
    if not versions:
        print(f"{lib}: no glibc symbol versions found", file=sys.stderr)
        return 1
    newest = max(versions)
    newest_str = ".".join(map(str, newest))
    if newest > FLOOR:
        print(f"{lib}: needs GLIBC_{newest_str}, newer than the {floor} floor", file=sys.stderr)
        return 1
    print(f"{lib}: needs at most GLIBC_{newest_str} (floor {floor})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
