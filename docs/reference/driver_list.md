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

# Driver List Reference

`dbc.toml` is the default filename dbc uses for a [driver list](../concepts/driver_list.md). This page outlines the structure of that file.

This file uses the [TOML](https://toml.io) file format and contains a single TOML Table called "drivers".
Each driver must have a name and may optionally have a version constraint and pre-release setting. See [Version Constraints](../guides/installing.md#version-constraints) to learn how to specify version constraints.

## Example

The following driver list specifies:

- Whatever is the latest stable version of the "mysql" driver
- The exact 1.4.0 version of the "duckdb" driver
- The latest stable version in the 1.x.x major series for the "postgresql" driver
- The latest version (including pre-releases) of the "snowflake" driver

```toml
[drivers]

[drivers.mysql]

[drivers.duckdb]
version = '=1.4.0'

[drivers.postgresql]
version = '=1.x.x'

[drivers.snowflake]
prerelease = 'allow'
```

## Fields

### `version`

Optional. A version constraint string that specifies which versions of the driver are acceptable. If omitted, dbc will use the latest stable version available.

See [Version Constraints](../guides/installing.md#version-constraints) for the full syntax.

### `prerelease`

{{ since_version('v0.2.0') }}

Optional. Controls whether pre-release versions should be considered during version resolution.

- When set to `'allow'`, dbc will consider pre-release versions when selecting which version to install
- When omitted or set to any other value, only stable (non-pre-release) versions will be considered

This field is typically set automatically when using `dbc add --pre`.

**Example:**

```toml
[drivers.mysql]
prerelease = 'allow'
```

**Interaction with version constraints:**

The `prerelease` field only affects implicit version resolution. When your `version` constraint unambiguously references a pre-release by including a pre-release suffix (like `version = '>=1.0.0-beta.1'`), pre-release versions will be considered regardless of this field.

However, if your version constraint is ambiguous and only pre-release versions satisfy it, `dbc sync` will fail unless `prerelease = 'allow'` is set. For example, if a driver has versions `0.1.0` and `0.1.1-beta.1`:

```toml
[drivers.mysql]
version = '>0.1.0'
# This will FAIL during sync, not install 0.1.1-beta.1
```

To allow the pre-release in this case, either:

- Add `prerelease = 'allow'`
- Change the constraint to reference the pre-release: `version = '>=0.1.1-beta.1'`

## Platforms

{{ since_version('v0.4.0') }}

Use the optional top-level `platforms` key to list the [platforms](supported_platforms.md) your project must support.

```toml
platforms = ["linux_amd64", "macos_arm64", "macos_amd64"]
```

When selecting a version, `dbc add`, `dbc lock` and `dbc sync` choose the newest allowed release with a build for every listed platform. When checking a registry candidate, dbc reports missing platform coverage as an error. If dbc needs a package for a listed platform and the selected release lists more than one, it reports an error instead of choosing an older release.

`dbc.lock` records an archive for each listed platform. If the registry publishes no hash for an archive, dbc downloads it to compute the hash. `dbc sync` requires the current platform to be listed. When the list changes, dbc keeps the recorded archives and fills missing platforms from the locked version and registry. If that version lacks a required build, dbc resolves the driver again and warns if the version or registry changes. If the locked registry uses a query string or no longer lists the driver, adding a missing archive fails.

Omit `platforms` to select versions without a platform requirement. An empty list is an error.

## Registries

{{ since_version('v0.4.0') }}

A driver list may declare additional [driver registries](../concepts/driver_registry.md#configuring-registries) to resolve drivers from, using one or more `[[registries]]` entries and an optional top-level `exclude_builtin_registries` key. These registries are used by `dbc add`, `dbc sync`, `dbc search`, `dbc info`, and `dbc docs` when run against this driver list.

```toml
exclude_builtin_registries = true

[[registries]]
url = "https://my-registry.example.com"
name = "my-registry"

[drivers]
[drivers.my-driver]
version = '>=1.0.0'
```

### `url`

Required. The base URL of the registry. Must be an `http` or `https` URL with a host, whose path has no empty, `.` or `..` segments and no escaped `/` (`%2F`).

### `name`

Optional. A short name for the registry, which must be unique within the driver list. A driver's [`source`](#source) can refer to it, and it is shown as a `[name]` tag in `dbc search` output to indicate which registry a driver came from.

### `source`

{{ since_version('v0.4.0') }}

Optional, per driver. The only registry the driver is resolved from, as an inline table with `type = "registry"` and exactly one of:

- `name`: the `name` of a `[[registries]]` entry in the same driver list. A driver list cannot refer to a name from the user `config.toml`.
- `url`: the URL of a configured registry (from the driver list, the user `config.toml` or the built-in registries), without user information, query or fragment. A trailing `/`, the case of the scheme and host, and the default port of the scheme do not matter.

```toml
[[registries]]
url = "https://drivers.example.com"
name = "example"

[drivers.my-driver]
source = { type = "registry", name = "example" }

[drivers.flightsql]
source = { type = "registry", url = "https://dbc-cdn.columnar.tech" }
```

dbc does not fall back to another registry when the selected registry does not have the driver, has no matching version or cannot be reached; each of these is an error. The source must select exactly one configured registry: registries that differ only in user information or query cannot be told apart in `dbc.lock`, so a source that matches more than one of them is an error. [`dbc add --registry`](cli.md#add) records a source.

`dbc.lock` records the registry a driver was resolved from. If the source then selects another registry, `dbc sync` stops and [`dbc lock`](cli.md#lock) resolves the driver again from the new registry. Naming the same registry another way (by name instead of URL, for example) keeps the lock entry.

### HTTP source

{{ since_version('v0.4.0') }}

Use `type = "http"` to download a dbc package archive (`.tar.gz` with a
`MANIFEST`) directly. A URL template does not tell dbc which versions are
available. Set `version` to a full semantic version, such as `"1.2.0"` or
`"=1.2.0"`. An omitted version, a prefix such as `"1.2"`, or a range such as
`">=1.2.0"` is a configuration error before any download.

In the URL, `{version}` expands to the full version without the optional `=`,
including any prerelease or build metadata. For example, `"1.2.0-beta.1"`
expands to `1.2.0-beta.1`. For tags with a leading `v`, write `v{version}` in
the URL. At installation, the archive's `MANIFEST` version must match the
requested version.

```toml
[drivers.example]
version = "1.2.0"
source = {
  type = "http",
  url = "https://example.com/releases/download/v{version}/example_{os}_{arch}_v{version}.tar.gz",
  checksum_url = "https://example.com/releases/download/v{version}/SHA256SUMS",
  signing_key = false,
}
```

- `url`: required. An HTTP(S) URL without user information, a query, or a
  fragment. Its path can contain `{version}`, `{os}`, and `{arch}`. The OS and
  architecture use dbc's [platform spellings](supported_platforms.md), such as
  `linux` and `amd64`.
- `checksum_url`: optional. An HTTP(S) URL with the same rules and variables
  as `url`. It must use HTTPS when `url` does. The file can contain a single
  SHA-256 hash if it serves one archive, or `sha256sum` lines matched by the
  filename in each archive URL.
- `signing_key`: a name from `[signing_keys]` in the same `dbc.toml`. Omit it
  to use dbc's built-in key, or set it to `false` for an unsigned package.

Unknown source keys are errors. `dbc lock` and `dbc sync` record archives for
the [listed platforms](#platforms), or for the current platform if
`platforms` is omitted.

For archives without a recorded hash, dbc reads the hash from `checksum_url`
if set. Otherwise, it pins the first download by recording its SHA-256 hash
and size. dbc extracts archives during installation or to check an applicable
v1 library checksum for the current platform. For signed packages, `dbc sync`
verifies the driver library's signature during installation and reuse unless
you pass `--no-verify`. dbc warns when a new hash comes from a plain HTTP URL
declared in the driver list.

Archive and checksum downloads use no registry credentials.
[URL replacements](../concepts/driver_registry.md#mirrors) from the user
`config.toml` apply. Replacements and redirects from HTTPS to HTTP are
rejected. `DBC_BASE_URL` does not affect HTTP sources.

### `exclude_builtin_registries`

Optional. When `true`, dbc's built-in registries are not used; the registries configured in this file and in the global `config.toml` still are. When omitted or `false`, the built-in registries are used unless the global `config.toml` sets `exclude_builtin_registries = true`, which a driver list cannot override. dbc warns when a driver list sets `false` while the global `config.toml` excludes the built-in registries.

See [Configuring registries](../concepts/driver_registry.md#configuring-registries) for the full priority order across project, global, and built-in registries.
