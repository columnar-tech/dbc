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

# Installing Drivers

Once you've [installed dbc](../getting_started/installation.md), the first thing you'll probably want to do is install a driver.
But before you can install a driver, you need to know what drivers are available and how to refer to them.

## Finding a Driver

To find out what drivers are available, use `dbc search`:

```console
$ dbc search
• bigquery - An ADBC driver for Google BigQuery developed by the ADBC Driver Foundry
• duckdb - An ADBC driver for DuckDB developed by the DuckDB Foundation
• flightsql - An ADBC driver for Apache Arrow Flight SQL developed under the Apache Software Foundation
• mssql - An ADBC driver for Microsoft SQL Server developed by Columnar
• mysql - An ADBC Driver for MySQL developed by the ADBC Driver Foundry
• postgresql - An ADBC driver for PostgreSQL developed under the Apache Software Foundation
• redshift - An ADBC driver for Amazon Redshift developed by Columnar
• snowflake - An ADBC driver for Snowflake developed under the Apache Software Foundation
• sqlite - An ADBC driver for SQLite developed under the Apache Software Foundation
```

The short names in lowercase on the left of the output are the names you need to pass to `dbc install`.

## Installing a Driver

To install a specific driver, such as `mysql`, run:

```console
$ dbc install mysql
[✓] searching
[✓] downloading
[✓] installing
[✓] verifying signature

Installed mysql 0.1.0 to /Users/user/Library/Application Support/ADBC/Drivers
```

## Version Constraints

By default, dbc installs the latest version of the package you specify.
To install a specific version, you can pass a version constraint with the name:

```console
$ dbc install "mysql=0.1.0"
```

The syntax for specifiying a version may be familiar to you if you've used other package managers.

!!! note
    dbc uses the [github.com/Masterminds/semver/v3](https://pkg.go.dev/github.com/Masterminds/semver/v3#section-readme) package whose README has a good overview of the syntax it allows. In short, you can use `=`, `!=`, `>`, `<`, `>=`, `<=`, `~`, `^`, ranges like `1.2 - 1.4.5`, and wildcards (`x`, `X`, or `*`).

## Updating a Driver

dbc doesn't offer a specific "update" or "upgrade" command but `dbc install` can do essentially the same thing.

For example, if you were to run `dbc install mysql` and get version 0.1.0, if — at some point in the future — version 0.2.0 were to be released, re-running `dbc install mysql` would upgrade your installed version to 0.2.0.

!!! note

    When dbc updates a driver like this, the old driver is uninstalled first. [ADBC driver manifests](../concepts/driver_manifest.md) provide a mechanism to support having multiple versions of the same driver installed at the same time and dbc may provide a convenient way to do this in a future release.

## Installing System Wide

By default, dbc installs drivers to the standard user-level ADBC driver path suitable for your system:

- macOS: `~/Library/Application Support/ADBC/Drivers`
- Linux: `~/.config/adbc/drivers`
- Windows: `%LOCAL_APPDATA%\ADBC\Drivers`

Numerous dbc subcommands, including `install`, accept an optional `--level` flag which can used to install drivers system-wide. Note that we run this command with `sudo` because otherwise the directory may not be writable:

```console
$ sudo dbc install --level system mysql
[✓] searching
[✓] downloading
[✓] installing
[✓] verifying signature

Installed mysql 0.1.0 to /Library/Application Support/ADBC/Drivers
```

Where this installs depends on your operating system:

- macOS: `/Library/Application Support/ADBC/Drivers`
- Linux: `/etc/adbc/drivers`
- Windows: `C:\Program Files\ADBC\Drivers`

!!! note

    See [Manifest Location and Discovery](https://arrow.apache.org/adbc/main/format/driver_manifests.html#manifest-location-and-discovery) for complete documentation of where the ADBC driver managers will search for drivers. dbc has the same behavior.


!!! note

    Also see the [Config Level](../reference/config_level.md) reference for more detail on this behavior.

## `ADBC_DRIVER_PATH`

For complete control over where dbc installs drivers, set the `ADBC_DRIVER_PATH` environment variable to a path (or list of paths) where you want to install drivers.
For example:

```console
$ mkdir "$HOME/drivers"
$ export ADBC_DRIVER_PATH="$HOME/drivers"
$ dbc install mysql

[✓] searching
[✓] downloading
[✓] installing
[✓] verifying signature

Installed mysql 0.1.0 to /home/user/drivers

$ tree $ADBC_DRIVER_PATH
/home/user/drivers
├── mysql_linux_amd64_v0.1.0
│   ├── libadbc_driver_mysql.so
│   ├── libadbc_driver_mysql.so.sig
│   ├── LICENSE
│   └── NOTICE
└── mysql.toml

2 directories, 5 files
```

!!! note

    If you set `$ADBC_DRIVER_PATH` environment variable with dbc, you will also need to re-use the same shell or set it in your ADBC driver manager code explicitly. For example:

    ```python
    import os
    from pathlib import Path

    from adbc_driver_manager import dbapi

    os.environ["ADBC_DRIVER_PATH"] = str(Path.home() / "drivers")

    with dbapi.connect(driver="mysql") as con:
      pass
    ```

## Python Support

By default, dbc automatically detects whether you've activated a Python [virtual environment](https://docs.python.org/3/tutorial/venv.html) and will install (and uninstall) drivers from the virtual environment rather than the user or system-level paths.

```console
~/tmp/my-adbc-project
$ python3 -m venv .venv

~/tmp/my-adbc-project
$ source .venv/bin/activate.fish

~/tmp/my-adbc-project
.venv $ dbc install mysql
[✓] searching
[✓] downloading
[✓] installing
[✓] verifying signature

Installed mysql 0.1.0 to /Users/user/tmp/my-adbc-project/.venv/etc/adbc/drivers
```

!!! note

    [`ADBC_DRIVER_PATH`](#adbc_driver_path) takes precedence over a virtual environment. dbc (and [ADBC driver managers](../concepts/driver_manager.md)) use the following precedence hierarchy: `ADBC_DRIVER_PATH` before virtual enviroments before Conda environments.

## Conda Support

By default, dbc automatically detects whether you've activated a [Conda environment](https://docs.conda.io/projects/conda/en/latest/user-guide/concepts/environments.html) and will install (and uninstall) drivers from the Conda environment rather than the user or system-level paths.

```console
$ conda create -n my-adbc-project
$ conda activate my-adbc-project
my-adbc-project $ dbc install mysql
[✓] searching
[✓] downloading
[✓] installing
[✓] verifying signature

Installed mysql 0.1.0 to /opt/homebrew/Caskroom/miniforge/base/envs/my-adbc-project/etc/adbc/drivers
```

!!! note

    [`ADBC_DRIVER_PATH`](#adbc_driver_path) and/or an activated Python virtual environment will take precedence over a Conda environment. dbc (and [ADBC driver managers](../concepts/driver_manager.md)) use the following precedence hierarchy: `ADBC_DRIVER_PATH` before virtual enviroments before Conda environments.

## Uninstalling Drivers

You can uninstall a driver with the `dbc uninstall` subcommand.

```console
$ dbc uninstall mysql

Driver `mysql` uninstalled successfully!
```

Similar to the install command, `dbc uninstall` takes a `--level` argument.
If, for example, you installed a driver with `--level system`, you would want also pass that argument to `dbc uninstall`:

```console
$ sudo dbc install --level system mysql
$ sudo dbc uninstall --level system mysql
```

Since it's possible to install the same driver to multiple locations, dbc will only uninstall the first driver it finds.
dbc will search in the following order:

1. Environment
    1. `ADBC_DRIVER_PATH`
    2. `VIRTUAL_ENV`
    3. `CONDA_PREFIX`
2. User
3. System
