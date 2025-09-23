<!--
Copyright 2025 Columnar Technologies Inc.

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

# dbc.toml

`dbc.toml` is the default filename dbc uses for a [driver list](../concepts/driver_list.md). This page outlines the structure of that file.

This file uses the [TOML](https://toml.io) file format and contains a single TOML Table called "drivers".
Each driver must have a name and may optionally have a version constraint. See [Version Constraints](../guides/installing.md#version-constraints) to learn how to specify version constraints.

## Example

The following driver list specifies:

- Whatever is the latest version of the "mysql" driver
- The exact 1.3.2 version of the "duckdb" driver
- The latest version in the 1.x.x major series for the "postgresql" driver.

```toml
[drivers]
mysql
duckdb = "1.3.2"
postgresql = "1.x.x"
```
