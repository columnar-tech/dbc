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

# dbc <picture><img src="https://raw.githubusercontent.com/columnar-tech/dbc/refs/heads/main/resources/dbc_logo_animated_padded.png?raw=true" width="180" align="right" alt="dbc Logo"/></picture>

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://github.com/columnar-tech/dbc/blob/main/LICENSE)
[![GitHub release (latest SemVer)](https://img.shields.io/github/v/release/columnar-tech/dbc)](https://github.com/columnar-tech/dbc/releases)
[![npm](https://img.shields.io/npm/v/@columnar-tech/dbc)](https://www.npmjs.com/package/@columnar-tech/dbc)

**dbc is the command-line tool for installing and managing [ADBC](https://arrow.apache.org/adbc) drivers.**

## Install

```sh
npm install -g @columnar-tech/dbc
```

Or run without installing:

```sh
npx @columnar-tech/dbc --help
```

## Usage

Search for available drivers:

```sh
dbc search
```

Install a driver:

```sh
dbc install snowflake
```

Then use it with the [Node.js ADBC driver manager](https://arrow.apache.org/adbc/current/javascript/driver_manager.html):

```sh
npm install @apache-arrow/adbc-driver-manager apache-arrow
```

```typescript
import { AdbcDatabase } from '@apache-arrow/adbc-driver-manager'

const db = new AdbcDatabase({
  driver: 'snowflake',
  databaseOptions: {
    username: 'USER',
    password: 'PASS',
    'adbc.snowflake.sql.auth_type': 'auth_snowflake',
    'adbc.snowflake.sql.account': 'ACCOUNT-IDENT',
  },
})

let conn
try {
  conn = await db.connect()
  const table = await conn.query('SELECT * FROM CUSTOMER LIMIT 5')
  console.log(table.toString())
} finally {
  await conn?.close()
  await db.close()
}
```

## Other installation methods

dbc is also available via Homebrew, shell script, pip/uv, WinGet, and MSI installer.
See the [installation docs](https://docs.columnar.tech/dbc/getting_started/installation) for all options.

## Documentation

Full documentation at [docs.columnar.tech/dbc](https://docs.columnar.tech/dbc).

## Links

- [GitHub Repo](https://github.com/columnar-tech/dbc)
- [Issues](https://github.com/columnar-tech/dbc/issues)
- [Discussions](https://github.com/columnar-tech/dbc/discussions)
