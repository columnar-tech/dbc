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

# Finding Drivers

You can list the available drivers by running `dbc search`:

```console
$ dbc search
• bigquery - An ADBC driver for Google BigQuery developed by the ADBC Driver Foundry
• databricks - An ADBC Driver for Databricks developed by the ADBC Driver Foundry
• duckdb - An ADBC driver for DuckDB developed by the DuckDB Foundation
• flightsql - An ADBC driver for Apache Arrow Flight SQL developed under the Apache Software Foundation
• mssql - An ADBC driver for Microsoft SQL Server developed by Columnar
• mysql - An ADBC Driver for MySQL developed by the ADBC Driver Foundry
• postgresql - An ADBC driver for PostgreSQL developed under the Apache Software Foundation
• redshift - An ADBC driver for Amazon Redshift developed by Columnar
• snowflake - An ADBC driver for Snowflake developed under the Apache Software Foundation
• sqlite - An ADBC driver for SQLite developed under the Apache Software Foundation
• trino - An ADBC Driver for Trino developed by the ADBC Driver Foundry
```

## Finding a Specific Driver

You can filter the list of drivers by a pattern.
The pattern is treated as a regular expression using Go's [regexp/syntax](https://pkg.go.dev/regexp/syntax) syntax and is tested against both the name and the description of the driver.

For example, you can find drivers with 'sql' in their name by running,

```console
$ dbc search sql
• flightsql - An ADBC driver for Apache Arrow Flight SQL developed under the Apache Software Foundation
• mssql - An ADBC driver for Microsoft SQL Server developed by Columnar
• mysql - An ADBC Driver for MySQL developed by the ADBC Driver Foundry
• postgresql - An ADBC driver for PostgreSQL developed under the Apache Software Foundation
• sqlite - An ADBC driver for SQLite developed under the Apache Software Foundation
```

## Options

### Verbose

You can use the `--verbose` flag to show detailed information about each driver, including all versions that are available and which are installed.
```console
$ dbc search --verbose
• bigquery
   Title: ADBC Driver Foundry Driver for Google BigQuery
   Description: An ADBC driver for Google BigQuery developed by the ADBC Driver Foundry
   License: Apache-2.0
   Available Versions:
    ├── 1.0.0
    ╰── 1.10.0
• databricks
   Title: ADBC Driver Foundry Driver for Databricks
   Description: An ADBC Driver for Databricks developed by the ADBC Driver Foundry
   License: Apache-2.0
   Available Versions:
    ╰── 0.1.2
• duckdb
   Title: DuckDB Driver
   Description: An ADBC driver for DuckDB developed by the DuckDB Foundation
   License: MIT
   Available Versions:
    ├── 1.4.0
    ├── 1.4.1
    ├── 1.4.2
    ╰── 1.4.3
• flightsql
   Title: ASF Apache Arrow Flight SQL Driver
   Description: An ADBC driver for Apache Arrow Flight SQL developed under the Apache Software Foundation
   License: Apache-2.0
   Available Versions:
    ├── 1.8.0
    ├── 1.9.0
    ╰── 1.10.0
• mssql
   Title: Columnar Microsoft SQL Server Driver
   Description: An ADBC driver for Microsoft SQL Server developed by Columnar
   License: LicenseRef-PBL
   Available Versions:
    ├── 1.0.0
    ├── 1.1.0
    ╰── 1.2.0
• mysql
   Title: ADBC Driver Foundry Driver for MySQL
   Description: An ADBC Driver for MySQL developed by the ADBC Driver Foundry
   License: Apache-2.0
   Available Versions:
    ├── 0.1.0
    ╰── 0.2.0
• postgresql
   Title: ASF PostgreSQL Driver
   Description: An ADBC driver for PostgreSQL developed under the Apache Software Foundation
   License: Apache-2.0
   Available Versions:
    ├── 1.8.0
    ├── 1.9.0
    ╰── 1.10.0
• redshift
   Title: Columnar ADBC Driver for Amazon Redshift
   Description: An ADBC driver for Amazon Redshift developed by Columnar
   License: LicenseRef-PBL
   Available Versions:
    ├── 1.0.0
    ╰── 1.1.0
• snowflake
   Title: ASF Snowflake Driver
   Description: An ADBC driver for Snowflake developed under the Apache Software Foundation
   License: Apache-2.0
   Available Versions:
    ├── 1.8.0
    ├── 1.9.0
    ├── 1.10.0
    ╰── 1.10.1
• sqlite
   Title: ASF SQLite Driver
   Description: An ADBC driver for SQLite developed under the Apache Software Foundation
   License: Apache-2.0
   Available Versions:
    ├── 1.7.0
    ├── 1.8.0
    ├── 1.9.0
    ╰── 1.10.0
• trino
   Title: ADBC Driver Foundry Driver for Trino
   Description: An ADBC Driver for Trino developed by the ADBC Driver Foundry
   License: Apache-2.0
   Available Versions:
    ├── 0.1.0
    ╰── 0.2.0
```

### Pre-release Versions

By default, `dbc search` hides drivers that only have pre-release versions and excludes pre-release versions from the list of available versions. Pre-release versions follow semantic versioning conventions and include version identifiers like `1.0.0-alpha.1`, `2.0.0-beta.3`, or `1.5.0-rc.1`.

To include pre-release versions in search results, use the `--pre` flag:

```console
$ dbc search --pre
```

This will show drivers that only have pre-release versions and include pre-release versions in the available versions list when using `--verbose`.

For example, with `--pre --verbose`:

```console
$ dbc search --pre --verbose mysql
• mysql
   Title: ADBC Driver Foundry Driver for MySQL
   Description: An ADBC Driver for MySQL developed by the ADBC Driver Foundry
   License: Apache-2.0
   Available Versions:
    ├── 0.1.0
    ├── 0.2.0-beta.1
    ╰── 0.2.0
```

!!! note
    The `--pre` flag only affects the visibility of pre-release versions in search results. To actually install a pre-release version, you need to use `--pre` with `dbc install` or specify the exact pre-release version you want.
