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

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![GitHub release (latest SemVer)](https://img.shields.io/github/v/release/columnar-tech/dbc)](https://github.com/columnar-tech/dbc/releases)
[![Release dbc](https://github.com/columnar-tech/dbc/actions/workflows/release.yml/badge.svg)](https://github.com/columnar-tech/dbc/actions/workflows/release.yml)

**dbc is the command-line tool for installing and managing [ADBC](https://arrow.apache.org/adbc) drivers.**

## Install dbc

### Shell (Linux/macOS)

```
curl -LsSf https://dbc.columnar.tech/install.sh | sh
```

### Homebrew

```
brew install columnar-tech/tap/dbc
```

### uv

```
uv tool install dbc
```

### pipx

```
pipx install dbc
```

### PowerShell (Windows)

```
powershell -ExecutionPolicy ByPass -c irm https://dbc.columnar.tech/install.ps1 | iex
```

### WinGet

```
winget install dbc
```

### Windows MSI

[Download the MSI installer](https://dbc.columnar.tech/latest/dbc-latest-x64.msi)

For more installation options, see the [installation docs](docs/getting_started/installation.md).

## Getting Started

Search for available drivers:

```sh
dbc search
```

Install a driver:

```sh
dbc install snowflake
```

Use it with Python:

```sh
pip install "adbc-driver-manager>=1.8.0"
```

```python
import adbc_driver_manager.dbapi as adbc

with adbc.connect(
    driver="snowflake",
    db_kwargs={
        "username": "USER",
        "password": "PASS",
        "adbc.snowflake.sql.account": "ACCOUNT-IDENT",
        # ... other connection options
    },
) as con, con.cursor() as cursor:
    cursor.execute("SELECT * FROM CUSTOMER LIMIT 5")
    print(cursor.fetch_arrow_table())
```

You can also manage drivers in a project using a [driver list](docs/guides/driver_list.md). And you can store connection options in a [connection profile](https://arrow.apache.org/adbc/current/format/connection_profiles.html) instead of in your code.

 For more details, see the [dbc documentation](https://docs.columnar.tech/dbc) and the [ADBC Quickstarts](https://github.com/columnar-tech/adbc-quickstarts).

## Desktop GUI

A graphical desktop application is available in the [`gui/`](gui/) directory. It provides a visual interface for browsing the driver catalog, installing/uninstalling drivers, managing project driver lists, and authenticating with registries. See [`gui/README.md`](gui/README.md) for setup instructions.

## Communications

- [Discussions](https://github.com/columnar-tech/dbc/discussions) for questions
- [Issues](https://github.com/columnar-tech/dbc/issues) to report bugs or request features
- See [CONTRIBUTING.md](./CONTRIBUTING.md) for contributing

## Code of Conduct

By contributing to dbc, you agree to follow our [Code of Conduct](https://github.com/columnar-tech/.github/blob/main/CODE_OF_CONDUCT.md).
