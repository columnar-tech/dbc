<!-- Copyright (c) 2025 Columnar Technologies Inc.  All rights reserved. -->

<!--

Notes on how this document is structured:

- mkdocs doesn't let you omit some headers from the ToC so we use inline HTML
  instead
- mkdocs supports definition lists but not with links

-->

# CLI Reference

## dbc

<h3>Usage</h3>

```sh
dbc [OPTIONS] <COMMAND>
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
</dl>

## search

Search for a driver to install.

<h3>Usage</h3>

```sh
dbc search [PATTERN]
```

<h3>Arguments</h3>

`PATTERN`

:   Optional. A pattern to restrict the list of drivers returned by. Driver names are matched by wildcard so substrings may be used.

<h3>Options</h3>

`--verbose`, `-v`

:   Enable verbose output

`--namesonly`, `-n`

:   Restrict search to names, ignoring descriptions

## install

Install a driver.

To install multiple versions of the same driver on the same system, it's recommend to use `ADBC_DRIVER_PATH`. See [Config Level](config_level.md).

<h3>Usage</h3>

```sh
dbc install [OPTIONS] <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Name of the driver to install. Can be a short driver name or a driver name with version requirement. Examples: `bigquery`, `bigquery=1.0.0`, `bigquery>1`.

<h3>Options</h3>

`--level`

:   The configuration level to install the driver to (`user`, or `system`). See [Config Level](config_level.md).

`--no-verify`

:   Allow installation of drivers without a signature file

## uninstall

Uninstall a driver.

<h3>Usage</h3>

```sh
dbc uninstall [OPTIONS] <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Name of the driver to uninstall.

<h3>Options</h3>

`--level`

:   The configuration level to uninstall the driver from (`user`, or `system`). See [Config Level](config_level.md).

## init

Create a [driver list](../concepts/driver_list.md) file.

<h3>Usage</h3>

```sh
dbc init [PATH]
```

<h3>Arguments</h3>

`PATH`

:   Optional. A path to create a [driver list](../concepts/driver_list.md) under. Defaults to the current working directory.

## add

Add a driver to a current [driver list](../concepts/driver_list.md).

<h3>Usage</h3>

```sh
dbc add <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Name of the driver to add. Can be a short driver name or a driver name with version requirement. Examples: `bigquery`, `bigquery=1.0.0`, `bigquery>1`.

<h3>Options</h3>

`--path FILE`, `-p FILE`

:   Driver list to add to [default: ./dbc.toml]

## remove

Remove a driver from the current [driver list](../concepts/driver_list.md).

<h3>Usage</h3>

```sh
dbc remove <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Name of the driver to remove.

<h3>Options</h3>

`--path FILE`, `-p FILE`

:   Driver list to add to [default: ./dbc.toml]

## sync

Install drivers from a [driver list](../concepts/driver_list.md).
Also creates a `dbc.lock` file next to the [driver list](../concepts/driver_list.md).
If `dbc.lock` exists, driver versions from it will be used when this subcommand is run.

<h3>Usage</h3>

```sh
dbc sync
dbc sync --file dbc.toml
```

<h3>Options</h3>

`--path`

:   Path to a [driver list](../concepts/driver_list.md) file to sync. Defaults to `dbc.toml` in the current working directory.

`--level`

:   The configuration level to install drivers to (`user`, or `system`). See [Config Level](config_level.md).

`--no-verify`

:   Allow installation of drivers without a signature file
