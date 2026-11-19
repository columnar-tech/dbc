<!--
Copyright 2026 Columnar Technologies Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# Driver Registry

dbc installs drivers from a "driver registry" which is an internet-accessible index of installable [ADBC driver](./driver.md) packages.

By default, dbc is configured to communicate with two driver registries: the public ADBC driver registry, which is affiliated with the [ADBC Driver Foundry](https://adbc-drivers.org) and hosted by Columnar, and Columnar's private driver registry. Most drivers will be from the public registry but some will be marked with a `[private]` label which means they're from the private registry. See [Private Drivers](../guides/private_drivers.md) for information on how to install and use private drivers.

- Public: [https://dbc-cdn.columnar.tech](https://dbc-cdn.columnar.tech)
- Private: [https://dbc-cdn-private.columnar.tech](https://dbc-cdn-private.columnar.tech)

!!! note

    If you access these registries without dbc, please set a user agent. See [Can I download drivers directly from the driver registries?](../getting_started/faq.md#can-i-download-drivers-directly-from-the-driver-registries).

When you run a command like [`dbc search`](../reference/cli.md#search) or [`dbc install`](../reference/cli.md#install), dbc gets information about the drivers that are available from each configured registry by downloading its `index.yaml` or using a cached copy.

## Mirrors

{{ since_version('v0.4.0') }}

In a network that can only reach the registries through a mirror (for example a
repository manager such as Artifactory), tell dbc where requests for a registry
should go instead. Add `network.url_replacements` rules to your user
`config.toml`:

```toml
[[network.url_replacements]]
prefix = "https://dbc-cdn.columnar.tech"
replacement = "https://artifactory.example.com/dbc-cdn"
```

The file lives in the `dbc` directory below the `Columnar` (`columnar` on Linux)
folder of your user configuration directory, for example
`~/.config/columnar/dbc/config.toml` on Linux,
`~/Library/Application Support/Columnar/dbc/config.toml` on macOS and
`%AppData%\Columnar\dbc\config.toml` on Windows.

- A request matches a rule when the scheme, host and port are the same as the
  prefix's and the prefix path is a prefix of the request path at a path
  segment boundary. Percent-encoded unreserved ASCII characters (letters,
  digits, `-`, `.`, `_` and `~`) compare as their literal characters. Reserved
  escapes such as `%2F` remain escaped and do not become path separators. The
  matching part is replaced, so
  `https://dbc-cdn.columnar.tech/index.yaml` is requested from
  `https://artifactory.example.com/dbc-cdn/index.yaml`.
- Rules are checked in order and the first match wins. The result is never
  matched against the rules again.
- Both URLs must be absolute `http` or `https` URLs without credentials, query
  or fragment, and an `https` prefix cannot be replaced by an `http` URL. dbc
  refuses registry operations if the user config cannot be read or decoded,
  contains an invalid registry entry, or has an invalid replacement rule.
- The mirror must serve the same bytes as the registry. Requests to a mirror are
  sent without credentials and without the telemetry identifiers dbc normally
  adds. Archive hashes and signatures are checked exactly as without a mirror,
  so a mirror cannot change a driver unnoticed while signature verification is
  on or the archive hash is pinned in `dbc.lock`.
- dbc does not fall back to the original URL when the mirror fails, and refuses
  any redirect to a URL that a rule replaces, whether it comes from the mirror
  or from a site that no rule replaces. Native Go clients also reject
  HTTPS-to-HTTP redirects for replaced requests. In Wasm, the browser's `fetch`
  follows redirects internally, so dbc cannot apply these redirect checks.
- `dbc.lock` and `dbc.toml` keep the original registry URLs, so a lock file
  created behind a mirror can be shared with people who use the registry
  directly and the other way round.
- Only the user `config.toml` can set this. A `dbc.toml` with a `network` table
  is rejected, so a project cannot redirect your requests.
- Mirrors that require authentication, and registries whose URL has a query
  string, are not supported yet. If the mirror answers 401 or 403, dbc reports
  that the mirror refused access; logging in with `dbc auth login` does not
  help, because no credentials are sent to the mirror.

## Signing keys

{{ since_version('v0.4.0') }}

dbc verifies the signature of every driver it installs. By default it uses the
Columnar public key that is built into dbc, which signs the packages of the
default registries. A registry that publishes drivers signed with another
OpenPGP key names that key with `signing_key`, in your user `config.toml` or in
a project's `dbc.toml`:

```toml
[signing_keys.acme]
type = "openpgp"
public_key_file = "keys/acme.asc"

[[registries]]
url = "https://drivers.acme.example.com"
signing_key = "acme"
```

- `signing_key` names a `[signing_keys.<name>]` table in the same file. A name
  is never looked up in another file, so a project cannot use a key that is only
  defined in your user config, and the other way round. When `signing_key` is
  omitted, the registry's packages are verified with the Columnar key, also when
  the user config declares the same registry with another key: the entry of the
  file that declares the registry first (the project, then the user config) is
  used as a whole.
- `signing_key = false` declares that the registry's packages are not signed.
  They are installed without a signature check; the archive hash that the
  registry publishes is still checked.
- A `[signing_keys.<name>]` table has `type = "openpgp"` and exactly one of
  `public_key`, an ASCII-armored public key written in the file, and
  `public_key_file`, the path of a file holding the key, armored or binary.
  A relative path is relative to the directory of the file that defines it,
  whatever the working directory; `~` and environment variables are not
  expanded. The name `columnar` is reserved for the built-in key.
- The key must be exactly one public key (certificate): private keys, files with
  several keys such as a keyring, and data after the key are rejected. Export
  the one key you trust, for example with
  `gpg --export --armor <fingerprint> > keys/acme.asc`.
- A key written inline is checked when the file is loaded. A key file is read
  only when a registry that names it is used, so a missing file does not stop
  commands that do not need it; for those that do, it is an error, never a
  fallback to another key.
- In a project, prefer keys that travel with the project: written inline or in a
  file next to `dbc.toml` under version control. An absolute path such as
  `/usr/share/keyrings/acme.gpg` works, but only on machines that have the file;
  keep such paths in your user config.

`dbc.lock` records the fingerprint of the key that each driver's packages must
verify with, or `type = "none"` for a registry declared unsigned. `dbc sync`
and `dbc lock` refuse to use a lock entry when the configuration of its
registry now selects a different key, also with `--no-verify`, so changing the
key of a registry needs the lock file to be created again. The fingerprint
identifies the primary key only: a new signing subkey or a revocation of the
same primary key does not change it, and is used as soon as the key file or
inline key is updated.

The browser build of the bindings cannot read key files; give explicit
registries their key inline instead (see [Go Library](../guides/go_library.md#signing-keys)).

For Go library configuration and explicit registry overrides, see [Go Library](../guides/go_library.md).
