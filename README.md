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

# dbc <picture><img src="https://raw.githubusercontent.com/columnar-tech/dbc/refs/heads/main/resources/dbc_logo_animated_padded.png?raw=true" width="180" align="right" alt="dbc Logo"/></picture>

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![GitHub release (latest SemVer)](https://img.shields.io/github/v/release/columnar-tech/dbc)](https://github.com/columnar-tech/dbc/releases)
[![Release dbc](https://github.com/columnar-tech/dbc/actions/workflows/release.yml/badge.svg)](https://github.com/columnar-tech/dbc/actions/workflows/release.yml)

## Overview

**dbc is a command-line tool for installing and managing [ADBC](https://arrow.apache.org/adbc) drivers.**

dbc can:

* Install pre-built [ADBC](https://arrow.apache.org/adbc) drivers with a single command
* Install drivers in your user account, on the system, or in virtual environments
* Manage isolated, reproducible project environments with driver lists and lockfiles
* Run on macOS, Linux, and Windows
* Be installed in many ways (with pip, standalone installers, Docker images, and more)
* Work in CI/CD environments

## Installation

There are multiple ways to install dbc:

### From PyPI

For simple installation, we recommend the popular [pipx](https://pipx.pypa.io/stable/installation) tool which will automatically put it on your `PATH`:

```sh
pipx install dbc
```

You can also just test it out instead of installing it:

```sh
pipx run dbc
```

You can also use a virtual environment:

```sh
python -m venv .venv
source .venv/bin/activate
pip install dbc
```

### Standalone Installer

#### macOS or Linux

You can download the install script and execute it:

```sh
curl -LsSf https://dbc.columnar.tech/install.sh | sh
```

If your system doesn't have `curl` you can also use `wget`:

```sh
wget -q0- https://dbc.columnar.tech/install.sh | sh
```

If you want to inspect the script before use, you can simply run:

```sh
curl -LsSf https://dbc.columnar.tech/install.sh | less
```

#### Windows

Download the Windows graphical installer for your architecture:

| Architecture |  Installer                                              |
| ------------ | ------------------------------------------------------- |
| x64 (64-bit) | <https://dbc.columnar.tech/latest/dbc-latest-x64.msi>   |

Or use `irm` to download the install script and execute it with `iex`:

```sh
powershell -ExecutionPolicy ByPass -c "irm https://dbc.columnar.tech/install.ps1 | iex
```

Changing the [execution policy](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_execution_policies?view=powershell-7.4#powershell-execution-policies) allows running a script from the internet.

Of course, you can also inspect the script before use:

```sh
powershell -c "irm https://dbc.columnar.tech/install.ps1 | more"
```

### GitHub Releases

Release artifacts for dbc can also be downloaded directly from [GitHub Releases](https://github.com/columnar-tech/dbc/releases). Included in the artifacts are also
cryptographic signatures and a checksum file to ensure nothing has been tampered with.

Each release includes the following assets allowing you to install using your preferred method:

- `.tar.gz` or `.zip` archives containing the appropriate binary for all supported platforms and architectures
- `.deb` and `.rpm` installation packages
- An `msi` installer package for Windows
- `.snap` packages
- Python wheel packages that bundle the dbc executable binary

### Docker

Docker images are also provided with standalone binaries that can be easily run using:

```sh
docker run --rm -it columnar/dbc:latest --help
```

#### Available Images

The following distroless images are available for linux-based `amd64` and `arm64`
architectures:

- `columnar/dbc:latest`
- `columnar/dbc:{major}.{minor}.{patch}`, e.g. `columnar/dbc:0.0.1`

## Homebrew

dbc is available via the Columnar Homebrew tap. The tap will first need
to be added to your environment:

```sh
brew tap columnar-tech/tap
```

Once you've done this you can install the `dbc` cask:

```sh
brew install --cask dbc
```

## Getting Started

Once you have dbc available to you on the command line, you can install an ADBC
driver and make it available to your user. For example, to install the bigquery driver:

```sh
dbc install bigquery
```

Alternately, when working on a project you can create a `dbc.toml` file to create a
list of drivers to install to create a reproducible environment:

```sh
cd <path/to/project>
dbc init # creates dbc.toml
dbc add bigquery # adds this to the driver list
dbc sync # install drivers and create dbc.lock
```

Using `dbc add` also allows version constraints:

```sh
dbc add "bigquery>=1.0.0"
dbc sync # looks for and installs a version >=1.0.0
```

### Using the Driver

The simplest way to use the driver is via Python with [`adbc-driver-manager`](https://pypi.org/project/adbc-driver-manager/).
*Note: version 1.8.0 added support for driver manifests, so you'll need that version of the driver manager or higher*.

```sh
dbc install snowflake
pip install "adbc-driver-manager>=1.8.0"
```

Using the driver is easy:

```python
import adbc_driver_manager.dbapi as adbc

snowflake_connect_args = {
    "username": "USER",
    "password": "PASS",
    "adbc.snowflake.sql.account": "ACCOUNT-IDENT",
    "adbc.snowflake.sql.db": "SNOWFLAKE_SAMPLE_DATA",
    # other connect options
}

with adbc.connect(
    driver="snowflake",
    db_kwargs=snowflake_connect_args,
) as con, con.cursor() as cursor:
    cursor.execute("SELECT * FROM CUSTOMER LIMIT 5")
    table = cursor.fetch_arrow_table()

print(table)
```

For more detailed information on using dbc, see the [documentation](https://docs.columnar.tech/dbc). Also check out the [ADBC Quickstarts](https://github.com/columnar-tech/adbc-quickstarts) repo to learn how to use ADBC with a variety of languages and databases.

## Communications

For general questions and discussion, use the GitHub [discussions](https://github.com/columnar-tech/dbc/discussions).

To report an issue, request a feature, or contribute an improvement, use the GitHub
[issues](https://github.com/columnar-tech/dbc/issues) and
[PRs](https://github.com/columnar-tech/dbc/pulls).

See [CONTRIBUTING.md](./CONTRIBUTING.md) for more information on contributing.

## Code of Conduct

By choosing to contribute to dbc, you agree to follow our [Code of Conduct](https://github.com/columnar-tech/.github/blob/main/CODE_OF_CONDUCT.md).
