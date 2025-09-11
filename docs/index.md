<!-- Copyright (c) 2025 Columnar Technologies.  All rights reserved. -->

# dbc

dbc is a command-line tool that makes installing and managing [ADBC](https://arrow.apache.org/adbc) drivers easy as 1, 2, 3.

```console
$ dbc install bigquery
$ python
>>> from adbc_driver_manager import dbapi
>>> con = dbapi.connect(driver="bigquery")
```

## Features

- Install pre-built [ADBC](https://arrow.apache.org/adbc) drivers with a single command
- Manage numerous drivers without conflicts
- Install drivers just for your user or system-wide
- Create reproducible environments with [drivers list](concepts/drivers_list.md) files
- Cross-platform: Runs on macOS, Linux, and Windows
- Installable with pip, Docker, and more (See [Installation](getting_started/installation.md))
- Works great in CI/CD environments

## Installation

Install dbc with our automated installer:

=== "macOS and Linux"

    ```sh
    curl -LsSf https://dbc.columnar.tech/install.sh | sh
    ```

=== "Windows"

    ```sh
    powershell -ExecutionPolicy ByPass -c "irm https://dbc.columnar.tech/install.ps1 | iex
    ```

Then, take your [first steps](getting_started/first_steps.md) to get started using dbc.

!!! note

    See our [Installation](getting_started/installation.md) page for more ways to get dbc.
