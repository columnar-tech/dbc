<!-- Copyright (c) 2025 Columnar Technologies Inc.  All rights reserved. -->

# Config Level

Various dbc subcommands (like [install](cli.md#install), [sync](cli.md#sync)) take a `--level` argument which gives you control over where dbc searches for drivers.

## Default Behavior

When the `--level` argument is not explicitly set for the command you are running, dbc defaults to searching a list of environment variables before searching the [User](#user) and [System](#system) levels.
When `--level` is provided, dbc searches _only_ the provided level and will also ignore any environment variables that may be set.

The following environment variables are searched, in order:

1. `ADBC_DRIVER_PATH`: When set, installs or locates drivers at `$ADBC_DRIVER_PATH`.
2. `VIRTUAL_ENV`: When set, installs or locates drivers at `$VIRTUAL_ENV/etc/adbc/drivers`. This variable is automatically set when you have activated a [Python virtual environment](https://docs.python.org/3/tutorial/venv.html).
3. `CONDA_PREFIX`: When set, installs or locates drivers at `$CONDA_PREFIX/etc/adbc/drivers`. This variable is automatically set when you have activated a [Conda environment](https://docs.conda.io/projects/conda/en/latest/user-guide/concepts/environments.html).

Note that dbc will stop searching for a driver when one is found.
For example, if you are in a Python virtual environment, you can still override the location where dbc installs or locates drivers by setting `$ADBC_DRIVER_PATH` to a directory of your choice.

## User

`--level` value `user`.

- On Linux (and other Unix-like platforms), this is `$XDG_CONFIG_HOME/adbc/drivers` (if `$XDG_CONFIG_HOME` is set) or `~/.config/adbc/drivers`.
- On macOS, this is `~/Library/Application Support/ADBC/Drivers`.
- On Windows, this is either the registry under `HKEY_CURRENT_USER\SOFTWARE\ADBC\Drivers\` or `%LOCAL_APPDATA%\ADBC\drivers`.

## System

`--level` value `system`.

!!! note

    Depending on your environment, you may need elevated privileges to use the `--level system` option (such as `sudo` on Unix-likes and Administrator on Windows).

- On Linux (and other Unix-like platforms), this is `/etc/adbc/drivers`.
- On macOS, this is `/Library/Application Support/ADBC/Drivers`.
- On Windows, this is in the registry under `HKEY_LOCAL_MACHINE\SOFTWARE\ADBC\Drivers\`

## More Info

See [ADBC Driver Manager and Manifests](https://arrow.apache.org/adbc/main/format/driver_manifests.html) for more detail.
