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

"""Stage a GoReleaser-built shared library for Linux development packages.

Run by the post hook of the `capi-shared` build in .goreleaser.yaml:
  python3 scripts/stage_devel.py <libdbc.so> <goarch> <version>

Copies the library to dist/devel/linux_<goarch>/libdbc.so and writes dbc.deb.pc
and dbc.rpm.pc with the library directories used by each package format.
"""

import pathlib
import shutil
import sys

REPO = pathlib.Path(__file__).resolve().parent.parent
DEB_LIBDIRS = {
    "amd64": "/usr/lib/x86_64-linux-gnu",
    "arm64": "/usr/lib/aarch64-linux-gnu",
}


def main() -> int:
    if len(sys.argv) != 4:
        print(__doc__, file=sys.stderr)
        return 2
    library = pathlib.Path(sys.argv[1])
    goarch = sys.argv[2]
    version = sys.argv[3]
    deb_libdir = DEB_LIBDIRS.get(goarch)
    if deb_libdir is None:
        print(f"error: no development package for linux/{goarch}", file=sys.stderr)
        return 1

    staged = REPO / "dist" / "devel" / f"linux_{goarch}"
    staged.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(library, staged / "libdbc.so")
    for package_format, libdir in (("deb", deb_libdir), ("rpm", "/usr/lib64")):
        (staged / f"dbc.{package_format}.pc").write_text(
            "prefix=/usr\n"
            f"libdir={libdir}\n"
            "includedir=${prefix}/include\n"
            "\n"
            "Name: dbc\n"
            "Description: C API of dbc, the ADBC driver installer\n"
            "URL: https://columnar.tech/dbc\n"
            f"Version: {version}\n"
            "Libs: -L${libdir} -ldbc\n"
            "Libs.private: -lpthread\n"
            "Cflags: -I${includedir}\n",
            encoding="utf-8",
        )
    print(f"staged into {staged}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
