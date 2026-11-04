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

"""Upload the Windows libdbc-dev zip to Cloudsmith as a raw package.

GoReleaser's cloudsmiths publisher only handles deb/rpm/apk, so the
`cloudsmith-raw` custom publisher in .goreleaser.yaml runs this for stable
releases:
  python3 scripts/publish_cloudsmith_raw.py --version 0.4.0 dist/libdbc-dev_0.4.0_windows_amd64.zip

Reads the API key from CLOUDSMITH_TOKEN. Uses Cloudsmith's two-step upload
(PUT the file, then create the raw package) and republishes, so re-running a
release replaces the package instead of failing.
"""

import argparse
import json
import os
import pathlib
import sys
import urllib.request

OWNER, REPOSITORY = "columnar", "dbc"
PACKAGE = "libdbc-dev-windows-x64"


def request(method: str, url: str, token: str, data: bytes, content_type: str) -> dict:
    req = urllib.request.Request(url, data=data, method=method, headers={
        "X-Api-Key": token,
        "Content-Type": content_type,
        "User-Agent": "dbc-release (https://github.com/columnar-tech/dbc)",
    })
    with urllib.request.urlopen(req, timeout=300) as response:
        return json.load(response)


def main() -> int:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument("--version", required=True, help="release version, e.g. 0.4.0")
    parser.add_argument("archive", type=pathlib.Path, help="libdbc-dev Windows zip")
    args = parser.parse_args()
    version = args.version.removeprefix("v")

    token = os.environ.get("CLOUDSMITH_TOKEN")
    if not token:
        print("error: CLOUDSMITH_TOKEN is not set", file=sys.stderr)
        return 1
    archive = args.archive
    if not archive.is_file():
        print(f"error: {archive} is missing", file=sys.stderr)
        return 1

    uploaded = request("PUT", f"https://upload.cloudsmith.io/{OWNER}/{REPOSITORY}/{archive.name}",
                       token, archive.read_bytes(), "application/octet-stream")
    package = request("POST", f"https://api.cloudsmith.io/v1/packages/{OWNER}/{REPOSITORY}/upload/raw/",
                      token, json.dumps({
                          "package_file": uploaded["identifier"],
                          "name": PACKAGE,
                          "version": version,
                          "summary": "Static library and headers for the C API of dbc (Windows x64)",
                          "description": "libdbc.lib, libdbc.h, and dbc.pc for dbc's C API. "
                                         "See https://github.com/columnar-tech/dbc.",
                          "republish": True,
                      }).encode(), "application/json")
    print(f"published {package.get('cdn_url') or archive.name}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
