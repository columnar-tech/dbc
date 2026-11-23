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

## Configuring registries

{{ since_version('v0.4.0') }}

Alongside the built-in registries, you can configure dbc to use additional driver registries at two levels:

- **Global**: a `config.toml` in your user config directory applies to every dbc command.
- **Project**: a `[[registries]]` section in a project's [`dbc.toml`](../reference/driver_list.md#registries) applies to dbc commands run in that project.

The global `config.toml` lives in your user config directory:

- Linux: `~/.config/columnar/dbc/config.toml`
- macOS: `~/Library/Application Support/Columnar/dbc/config.toml`
- Windows: `%AppData%\Columnar\dbc\config.toml`

Both files use the same `[[registries]]` syntax. Each entry requires a `url` (which must be an `http` or `https` URL) and may include an optional `name`, unique within the file, that is shown as a `[name]` tag in `dbc search` output:

```toml
[[registries]]
url = "https://my-registry.example.com"
name = "my-registry"
```

### Priority and conflicts

dbc uses the registries in this order, highest priority first:

1. Project registries (`dbc.toml`)
2. Global registries (`config.toml`)
3. Built-in registries

A URL that appears more than once is used only once, at its highest-priority position. When more than one registry publishes a driver with the same name, dbc installs it from the highest-priority registry that has a version matching the request. This fallback only happens between registries whose packages are verified with the same signing key: if registries with different keys publish the same driver name, whatever their versions, dbc reports an error instead of choosing one, so adding a registry cannot silently change who signs a driver.

To choose where a driver comes from, dbc compares every registry that might publish the driver. A registry whose index cannot be fetched or read (for example because access is denied, it is not found, the request times out or the server fails) is skipped when its packages are verified with the same signing key as those of the registries that publish the driver, since it could not change who signs it. Otherwise, [`dbc add`](../reference/cli.md#add), [`dbc install`](../reference/cli.md#install), [`dbc lock`](../reference/cli.md#lock) and [`dbc sync`](../reference/cli.md#sync) stop with an error that names the registry instead of resolving from the others. This does not apply to a lock entry that still satisfies the driver list, which is used without fetching every index, nor to drivers with a source and installs with `--registry`, which only need their registry. [`dbc search`](../reference/cli.md#search) still lists the drivers of the registries it could reach.

To take a driver from one registry only, set its [`source`](../reference/driver_list.md#source) in the driver list, or pass `--registry` to [`dbc add`](../reference/cli.md#add) or [`dbc install`](../reference/cli.md#install). A `name` in `[[registries]]` lets a source refer to the registry; it is also shown as the registry's `[name]` tag in `dbc search` output, taking precedence over the name the registry's index gives itself.

### Excluding the built-in registries

Set `exclude_builtin_registries = true` to remove the built-in registries and use only the registries you configure:

```toml
exclude_builtin_registries = true

[[registries]]
url = "https://my-registry.example.com"
name = "my-registry"
```

- The built-in registries are excluded when either the global `config.toml` or the project's `dbc.toml` sets `exclude_builtin_registries = true`. A project cannot bring them back with `false` once the global config excludes them; commands that read the project's `dbc.toml` then print a warning that its `false` has no effect.
- Only the built-in registries are removed. Global and project `[[registries]]` entries stay, including an entry that names a built-in registry's URL.
- dbc reports an error if no registry is left, for example when `exclude_builtin_registries = true` is set and neither file has a `[[registries]]` entry.

### Which commands use which registries

- Global `config.toml` registries are used by **every** dbc command.
- Project `dbc.toml` registries are used by the commands that manage or discover drivers for the current project — [`dbc add`](../reference/cli.md#add), [`dbc sync`](../reference/cli.md#sync), [`dbc search`](../reference/cli.md#search), [`dbc info`](../reference/cli.md#info), and [`dbc docs`](../reference/cli.md#docs) — which read `dbc.toml` from the current directory.
- [`dbc install`](../reference/cli.md#install) installs into a user- or system-level [config level](../reference/config_level.md) rather than the project, so it uses only the global and built-in registries and does not read a project `dbc.toml`.

Setting the [`DBC_BASE_URL`](../reference/environment_variables.md#dbc_base_url) environment variable overrides all registry configuration and points dbc at a single registry.

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

For Go library configuration and explicit registry overrides, see [Go Library](../guides/go_library.md).
