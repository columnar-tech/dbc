<!-- Copyright (c) 2025 Columnar Technologies Inc.  All rights reserved. -->

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
