<!-- Copyright (c) 2025 Columnar Technologies.  All rights reserved. -->

# dbc.toml

The format of the `dbc.toml`file  created by dbc is [TOML](https://toml.io) and it contains a single TOML Table called "drivers".
Each driver must have a name and may optionally have a version.

For example,

```toml
[drivers]
mysql
duckdb = "1.3.2"
```

TODO: Verify how this works once bugs are fixed and features are added.
