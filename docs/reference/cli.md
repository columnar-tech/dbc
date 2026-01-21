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

<!--

Notes on how this document is structured:

- mkdocs doesn't let you omit some headers from the ToC so we use inline HTML
  instead
- mkdocs supports definition lists but not with links

-->

# CLI Reference

## dbc

<h3>Usage</h3>

```console
$ dbc [OPTIONS] <COMMAND>
```

<h2>Commands</h2>

<dl class="cli-overview">
<dt><a href="#search">dbc search</a></dt><dd><p>Search for a driver to install</p></dd>
<dt><a href="#install">dbc install</a></dt><dd><p>Install a driver</p></dd>
<dt><a href="#uninstall">dbc uninstall</a></dt><dd><p>Uninstall a driver</p></dd>
<dt><a href="#init">dbc init</a></dt><dd><p>Create a <a href="../../concepts/driver_list/">driver list</a> file</p></dd>
<dt><a href="#add">dbc add</a></dt><dd><p>Add a driver to the <a href="../../concepts/driver_list/">driver list</a></p></dd>
<dt><a href="#remove">dbc remove</a></dt><dd><p>Remove a driver from the <a href="../../concepts/driver_list/">driver list</a></p></dd>
<dt><a href="#sync">dbc sync</a></dt><dd><p>Install the drivers from the <a href="../../concepts/driver_list/">driver list</a></p></dd>
<dt><a href="#info">dbc info</a></dt><dd><p>Get information about a driver</p></dd>
<dt><a href="#docs">dbc docs</a></dt><dd><p>Open driver documentation in a web browser</p></dd>
<dt><a href="#auth">dbc auth</a></dt><dd><p>Manage driver registry credentials</p></dd>
</dl>

## search

Search for a driver to install.

<h3>Usage</h3>

```console
$ dbc search [FILTER]
```

<h3>Arguments</h3>

`PATTERN`

:   Optional. A pattern to restrict the list of drivers returned by. Driver names are matched by wildcard so substrings may be used.

<h3>Options</h3>

`--json`

:   Print output as JSON instead of plaintext

`--verbose`, `-v`

:   Enable verbose output

`--quiet`, `-q`

:   Suppress all output

## install

Install a driver.

To install multiple versions of the same driver on the same system, it's recommend to use `ADBC_DRIVER_PATH`. See [Config Level](config_level.md).

<h3>Usage</h3>

```console
$ dbc install [OPTIONS] <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Name of the driver to install. Can be a short driver name or a driver name with version requirement. Examples: `bigquery`, `bigquery=1.0.0`, `bigquery>1`.

<h3>Options</h3>

`--json`

:   Print output as JSON instead of plaintext

`--level LEVEL`, `-l LEVEL`

:   The configuration level to install the driver to (`user`, or `system`). See [Config Level](config_level.md).

`--no-verify`

:   Allow installation of drivers without a signature file

`--quiet`, `-q`

:   Suppress all output

## uninstall

Uninstall a driver.

<h3>Usage</h3>

```console
$ dbc uninstall [OPTIONS] <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Name of the driver to uninstall.

<h3>Options</h3>

`--json`

:   Print output as JSON instead of plaintext

`--level LEVEL`, `-l LEVEL`

:   The configuration level to uninstall the driver from (`user`, or `system`). See [Config Level](config_level.md).

`--quiet`, `-q`

:   Suppress all output

## init

Create a [driver list](../concepts/driver_list.md) file.

<h3>Usage</h3>

```console
$ dbc init [PATH]
```

<h3>Arguments</h3>

`PATH`

:   Optional. A path to create a [driver list](../concepts/driver_list.md) under. Defaults to the current working directory.

<h3>Options</h3>

`--quiet`, `-q`

:   Suppress all output

## add

Add a driver to a current [driver list](../concepts/driver_list.md).

<h3>Usage</h3>

```console
$ dbc add <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Name of the driver to add. Can be a short driver name or a driver name with version requirement. Examples: `bigquery`, `bigquery=1.0.0`, `bigquery>1`.

<h3>Options</h3>

`--path FILE`, `-p FILE`

:   Driver list to add to [default: ./dbc.toml]

`--quiet`, `-q`

:   Suppress all output

## remove

Remove a driver from the current [driver list](../concepts/driver_list.md).

<h3>Usage</h3>

```console
$ dbc remove <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Name of the driver to remove.

<h3>Options</h3>

`--path FILE`, `-p FILE`

:   Driver list to remove from [default: ./dbc.toml]

`--quiet`, `-q`

:   Suppress all output

## sync

Install drivers from a [driver list](../concepts/driver_list.md).
Also creates a `dbc.lock` file next to the [driver list](../concepts/driver_list.md).
If `dbc.lock` exists, driver versions from it will be used when this subcommand is run.

<h3>Usage</h3>

```console
$ dbc sync
dbc sync --file dbc.toml
```

<h3>Options</h3>

`--path FILE`, `-p FILE`

:   Path to a [driver list](../concepts/driver_list.md) file to sync. Defaults to `dbc.toml` in the current working directory.

`--level LEVEL`, `-l LEVEL`

:   The configuration level to install drivers to (`user`, or `system`). See [Config Level](config_level.md).

`--no-verify`

:   Allow installation of drivers without a signature file

`--quiet`, `-q`

:   Suppress all output

## info

Get information about a driver. Shows information about the latest version of the driver with the given name.

<h3>Usage</h3>

```console
$ dbc info <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Name of the driver to get information for.

<h3>Options</h3>

`--json`

:   Print output as JSON instead of plaintext

`--quiet`, `-q`

:   Suppress all output

## docs

Open driver documentation in a web browser. If no driver is specified, opens the general dbc documentation. If a driver name is provided, opens the documentation for that specific driver.

<h3>Usage</h3>

```console
$ dbc docs
$ dbc docs <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Optional. Name of the driver to open documentation for. If omitted, opens the general dbc documentation page.

<h3>Options</h3>

`--no-open`

:   Print the documentation URL instead of opening it in a browser

`--quiet`, `-q`

:   Suppress all output

## auth

{{ since_version('v0.2.0') }}

<h3>Usage</h3>

```console
$ dbc auth
$ dbc auth login
$ dbc auth logout
```

<h3>Subcommands</h3>

### login

<h3>Arguments</h3>

`REGISTRYURL`

:   Optional. URL of the driver registry to authenticate with.

<h3>Options</h3>

`--clientid CLIENTID`

:   OAuth Client ID (can also be set via `DBC_OAUTH_CLIENT_ID`)

`--api-key API-KEY`

:   Authenticate using an API key instead of OAuth (use '-' to read from stdin)

### logout

<h3>Arguments</h3>

`REGISTRYURL`

:   Optional. URL of the driver registry to log out from

<h3>Options</h3>

`--purge`

:   Remove all local auth credentials for dbc
