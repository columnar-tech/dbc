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
