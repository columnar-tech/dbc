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
  a redirect from the mirror back to a URL that a rule replaces. Native Go
  clients also reject HTTPS-to-HTTP redirects for replaced requests. In Wasm,
  the browser's `fetch` follows redirects internally, so dbc cannot apply these
  redirect checks.
- `dbc.lock` and `dbc.toml` keep the original registry URLs, so a lock file
  created behind a mirror can be shared with people who use the registry
  directly and the other way round.
- Only the user `config.toml` can set this. A `dbc.toml` with a `network` table
  is rejected, so a project cannot redirect your requests.
- Mirrors that require authentication, and registries whose URL has a query
  string, are not supported yet.

For Go library configuration and explicit registry overrides, see [Go Library](../guides/go_library.md).
