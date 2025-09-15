# Searching for Drivers

## Searching

You can list the available drivers by running `dbc search`:

```console
$ dbc search
• duckdb - An analytical in-process SQL database management system
• snowflake - An ADBC driver for Snowflake developed under the Apache Software Foundation
• mssql - Columnar ADBC Driver for Microsoft SQL Server
• mysql - ADBC Driver Foundry Driver for MySQL
• flightsql - An ADBC driver for Apache Arrow Flight SQL developed under the Apache Software Foundation
```

## Finding a Specific Driver

You can filter the list of drivers by a pattern.
The pattern is treated as a regular expression using Go's [regexp/syntax](https://pkg.go.dev/regexp/syntax) syntax and is tested against both the name and the description of the driver.

For example, you can find drivers with 'sql' in their name by running,

```console
$ dbc search sql
• mssql - Columnar ADBC Driver for Microsoft SQL Server
• mysql - ADBC Driver Foundry Driver for MySQL
• flightsql - An ADBC driver for Apache Arrow Flight SQL developed under the Apache Software Foundation
```

## Options

### Verbose

You can use the `--verbose` flag to show detailed information about each driver. This will present information from the [ADBC Driver Manifest](https://arrow.apache.org/adbc/current/format/driver_manifests.html) that's packaged with the driver as well as all available and installed versions of the driver.

```console
$ dbc search --verbose
• duckdb
   Title: DuckDB
   Description: An analytical in-process SQL database management system
   License: MIT
   Available Versions:
    ╰── 1.3.2
• snowflake
   Title: ASF Snowflake Driver
   Description: An ADBC driver for Snowflake developed under the Apache Software Foundation
   License: Apache-2.0
   Available Versions:
    ╰── 1.7.0
• mssql
   Title: Columnar ADBC Driver for Microsoft SQL Server
   Description: Columnar ADBC Driver for Microsoft SQL Server
   License: LicenseRef-PBL
   Available Versions:
    ╰── 1.0.0
• mysql
   Title: ADBC Driver Foundry Driver for MySQL
   Description: ADBC Driver Foundry Driver for MySQL
   License: Apache-2.0
   Available Versions:
    ╰── 0.1.0
• flightsql
   Title: ASF Apache Arrow Flight SQL Driver
   Description: An ADBC driver for Apache Arrow Flight SQL developed under the Apache Software Foundation
   License: Apache-2.0
   Available Versions:
    ╰── 1.7.0
```

### Names Only

`dbc search` takes an optional `--names-only` (`-n` for short) flag which may be useful if you are scripting with dbc or are otherwise only interested in the names.

```console
$ dbc search --names-only
duckdb
snowflake
mssql
mysql
flightsql
```
