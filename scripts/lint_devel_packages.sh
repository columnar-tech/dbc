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

# Lint the libdbc-dev .deb files with lintian and the libdbc-devel .rpm files
# with rpmlint, each in a container of the distribution that defines its rules.
# Fails on any error or warning not suppressed below.
#
# Usage: scripts/lint_devel_packages.sh <directory with the packages>

set -euo pipefail

packages=$(cd "$1" && pwd)
repo=$(cd "$(dirname "$0")/.." && pwd)

# The package ships lintian overrides for its intentional findings (see the
# devel-deb entry in .goreleaser.yaml). initial-upload-closes-no-bugs applies
# only to uploads to the Debian archive, so it is suppressed here.
docker run --rm --mount "type=bind,source=$packages,target=/packages,readonly" \
  debian:stable bash -euo pipefail -c '
    apt-get -qq update >/dev/null
    apt-get -qq install -y --no-install-recommends lintian >/dev/null
    lintian --fail-on error,warning --tag-display-limit 0 \
      --suppress-tags initial-upload-closes-no-bugs \
      /packages/libdbc-dev_*.deb
  '

docker run --rm --mount "type=bind,source=$packages,target=/packages,readonly" \
  --mount "type=bind,source=$repo/resources/libdbc-devel.rpmlintrc,target=/rpmlintrc,readonly" \
  fedora:latest bash -euo pipefail -c '
    dnf -q -y install rpmlint >/dev/null
    # --strict fails on warnings too, matching lintian --fail-on above.
    rpmlint --strict -r /rpmlintrc /packages/libdbc-devel-*.rpm
  '
