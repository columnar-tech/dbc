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

# Config Level Reference

Various dbc subcommands (like [install](cli.md#install), [sync](cli.md#sync)) take a `--level` argument which gives you control over where dbc installs drivers.

## Default Behavior

When the `--level` argument is not explicitly set for the command you are running, dbc first searches a list of environment variables, before defaulting to the [User](#user) level.
When `--level` is explicitly set, dbc installs drivers in that level and ignores any environment variables that might be set.

dbc searches the following environment variables, in order:

1. `ADBC_DRIVER_PATH`: When set, installs drivers at `$ADBC_DRIVER_PATH`.
2. `VIRTUAL_ENV`: When set, installs drivers at `$VIRTUAL_ENV/etc/adbc/drivers`. This variable is automatically set when you have activated a [Python virtual environment](https://docs.python.org/3/tutorial/venv.html).
3. `CONDA_PREFIX`: When set, installs drivers at `$CONDA_PREFIX/etc/adbc/drivers`. This variable is automatically set when you have activated a [Conda environment](https://docs.conda.io/projects/conda/en/latest/user-guide/concepts/environments.html).

Note that dbc will stop searching for a driver installation location when one is found.
For example, if you are in a Python virtual environment, you can still override the location where dbc installs drivers by setting `$ADBC_DRIVER_PATH` to a directory of your choice.

## User

`--level` value `user`.

- On Linux (and other Unix-like platforms), this is `$XDG_CONFIG_HOME/adbc/drivers` (if `$XDG_CONFIG_HOME` is set) or `~/.config/adbc/drivers`.
- On macOS, this is `~/Library/Application Support/ADBC/Drivers`.
- On Windows, this is either the registry under `HKEY_CURRENT_USER\SOFTWARE\ADBC\Drivers\` or `%LOCAL_APPDATA%\ADBC\drivers`.

## System

`--level` value `system`.

!!! note

    Depending on your environment, you might need elevated privileges to use the `--level system` option (such as `sudo` on Unix-likes and Administrator on Windows).

- On Linux (and other Unix-like platforms), this is `/etc/adbc/drivers`.
- On macOS, this is `/Library/Application Support/ADBC/Drivers`.
- On Windows, this is in the registry under `HKEY_LOCAL_MACHINE\SOFTWARE\ADBC\Drivers\`

## More Info

See [ADBC Driver Manager and Manifests](https://arrow.apache.org/adbc/main/format/driver_manifests.html) for more detail.
