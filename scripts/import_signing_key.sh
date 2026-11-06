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

# Import the release signing key from $COLUMNAR_GPG_KEY, trust it, and preset
# $SIGNING_PASSWORD in gpg-agent so GoReleaser's gpg calls don't prompt.
# Portable to the Linux and macOS release runners.
set -euo pipefail

echo "$COLUMNAR_GPG_KEY" | gpg --batch --import
fingerprint=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/ { print $10; exit }')
printf 'trust\n5\ny\nquit\n' | gpg --batch --no-tty --command-fd 0 --edit-key "$fingerprint"

conf="${GNUPGHOME:-$HOME/.gnupg}/gpg-agent.conf"
{
  echo "default-cache-ttl 46000"
  echo "allow-preset-passphrase"
} >> "$conf"
gpg-connect-agent RELOADAGENT /bye

keygrip=$(gpg --list-secret-keys --with-colons --with-keygrip | awk -F: '/^grp:/ { print $10; exit }')
gpg-connect-agent "PRESET_PASSPHRASE $keygrip -1 $(printf '%s' "$SIGNING_PASSWORD" | od -An -t x1 | tr -d ' \n')" /bye
