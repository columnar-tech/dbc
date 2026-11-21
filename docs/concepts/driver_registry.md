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

Unless you pass `--no-verify`, dbc checks the signature of the drivers it
installs from a registry (except for registries declared unsigned, see below).
Drivers from the default registries are signed by Columnar, whose public key is
built into dbc. If you add a registry whose drivers are signed with another
OpenPGP key, name that key with `signing_key`, in your user `config.toml` or in
a project's `dbc.toml`.

You can write the public key in the file itself. This example works as is: it
configures the default public registry, at its own URL, with Columnar's key
(the same key that is built into dbc). For your own registry, use the key of
whoever signs its drivers.

```toml
[[registries]]
url = "https://dbc-cdn.columnar.tech"
signing_key = "columnar-release"

[signing_keys.columnar-release]
type = "openpgp"
public_key = '''
-----BEGIN PGP PUBLIC KEY BLOCK-----

mDMEaBUI6BYJKwYBBAHaRw8BAQdAh53LbNNOSjtg9fpT3pOy4M21CGZcxVAajlNz
g7+hQPO0S0NvbHVtbmFyIFRlY2hub2xvZ2llcywgSW5jIChTaWduaW5nIEtleSBm
b3IgQ29sdW1uYXIpIDxoZWxsb0Bjb2x1bW5hci50ZWNoPoiTBBMWCgA7FiEEq3dm
6bNBKZ68f0XdgqvQozo8nkgFAmgVCOgCGwMFCwkIBwICIgIGFQoJCAsCBBYCAwEC
HgcCF4AACgkQgqvQozo8nkgSrgD/Xrp9hgzXqo06UOyxNREWdcOtSkk4ztfCbfFx
Vns0J9QA/0wdIk4u7s6VpTttG53KC3h2+rbv9pnBTkPu6kWqSp0EuDgEaBUI6BIK
KwYBBAGXVQEFAQEHQHiCjDY7RXWnTBzut8b8raD95XO34Y630cI+uxvEOX5VAwEI
B4h4BBgWCgAgFiEEq3dm6bNBKZ68f0XdgqvQozo8nkgFAmgVCOgCGwwACgkQgqvQ
ozo8nkiwAQD+ISJowm+PCvEueTxWy2zbDmVO5CArdovL0wBgxAEqMDYBAIbQWYfd
dBcEhefDXhP/ZcbdsjznfjBSZYzK4FFyPxkE
=/096
-----END PGP PUBLIC KEY BLOCK-----
'''
```

Or keep the key in its own file, armored or binary. A relative path is relative
to the file that contains it. In a project's `dbc.toml`, the path must be
relative and stay inside the directory of `dbc.toml` (also through symbolic
links); only your user `config.toml` can name a key file elsewhere:

```toml
[[registries]]
url = "https://drivers.example.com"
signing_key = "example"

[signing_keys.example]
type = "openpgp"
public_key_file = "keys/example.asc"
```

- The key must be a single public key. Export only the key you trust, for
  example with `gpg --export --armor <fingerprint> > keys/example.asc`; a
  keyring with several keys is rejected.
- `signing_key` refers to a key defined in the same file. A project cannot use a
  key from your user config, and the other way round.
- Without `signing_key`, the registry is checked with Columnar's key.
- `signing_key = false` declares a registry whose drivers are not signed. dbc
  installs them without a signature check, but still checks the archive hash
  that the registry publishes. That hash comes from the same registry, so it
  catches a damaged download but cannot tell who published the driver: use
  this only for a registry you trust as a whole.
- A project's `dbc.toml` cannot change how a registry from your user config or
  a default registry is checked: declaring the same URL with another key, or
  unsigned, is an error. It can still add registries with their own keys or
  unsigned, so review changes to its registries and signing keys like code.
- In a project, keep the key inline or in a file committed next to `dbc.toml`,
  so that everyone who uses the project has it.

`dbc lock` records in `dbc.lock` which key each driver must be signed with, or
that its registry is unsigned. If you later point a registry at a different key
or make it unsigned, `dbc sync` and `dbc lock` stop with an error, also with
`--no-verify`; delete `dbc.lock` and run `dbc lock` again to accept the new key.

### What a signing key does not cover

- A key is trusted for every driver its registry serves, not for particular
  driver names. A registry with its own key, or unsigned, can also serve a
  driver under the same name as a default registry, and the first registry
  that has a suitable version is used, so adding a registry trusts it for any
  driver name. `dbc.lock` then pins each driver to the registry it was resolved
  from.
- The signature covers only the driver library. The manifest and the other
  files in the archive, which are installed next to the library, are covered
  only by the archive hash. Until `dbc.lock` records that hash, it is the hash
  the registry publishes, or the hash of the archive as downloaded when the
  registry publishes none; neither tells who made the archive.
- The signature does not name the driver or its version, so a registry can
  serve a library signed by its key under another name or version.
- An expired key does not stop anything: a signature dated while the key was
  valid can still verify after the key expires. A revocation takes effect only
  once the certificate in your configuration includes it. If a key leaks,
  update the configured certificate with the revocation, or replace or remove
  the key.

For Go library configuration and explicit registry overrides, see [Go Library](../guides/go_library.md).
