<!-- Copyright (c) 2025 Columnar Technologies.  All rights reserved. -->

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
<dt><a href="#init">dbc init</a></dt><dd><p>Create a <a href="../../concepts/drivers_list/">drivers list</a> file</p></dd>
<dt><a href="#add">dbc add</a></dt><dd><p>Add a driver to the <a href="../../concepts/drivers_list/">drivers list</a></p></dd>
<dt><a href="#remove">dbc remove</a></dt><dd><p>Remove a driver from the <a href="../../concepts/drivers_list/">drivers list</a></p></dd>
<dt><a href="#sync">dbc sync</a></dt><dd><p>Install the drivers from the <a href="../../concepts/drivers_list/">drivers list</a></p></dd>
</dl>

## search

Search for a driver to install.

<h3>Usage</h3>

```sh
dbc search [FILTER]
```

<h3>Arguments</h3>

`FILTER`

:   Optional. A pattern to restrict the list of drivers returned by. Driver names are matched by wildcard so substrings may be used.

## install

Install a driver.

To install multiple versions of the same driver on the same system, it's recommend to use `--level env` in conjunction with `ADBC_DRIVER_PATH`. See [Config](config.md).

<h3>Usage</h3>

```sh
dbc install [OPTIONS] <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Name of the driver to install. Can be a short driver name or a driver name with version requirement. Examples: `bigquery`, `bigquery=1.0.0`, `bigquery>1`.

<h3>Options</h3>

`--level`

:   The configuration level to install the driver to (`user`, or `system`). See [Config](config.md).

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

:   The configuration level to uninstall the driver from (`user`, or `system`). See [Config](config.md).

## init

Create a [drivers list](../concepts/drivers_list.md) file.

<h3>Usage</h3>

```sh
dbc init [PATH]
```

<h3>Arguments</h3>

`PATH`

:   Optional. A path to create the [drivers list](../concepts/drivers_list.md) under. Defaults to the current working directory.

## add

Add a driver to the current [drivers list](../concepts/drivers_list.md).

<h3>Usage</h3>

```sh
dbc add <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Name of the driver to add. Can be a short driver name or a driver name with version requirement. Examples: `bigquery`, `bigquery=1.0.0`, `bigquery>1`.

## remove

Remove a driver from the current [drivers list](../concepts/drivers_list.md).

<h3>Usage</h3>

```sh
dbc remove <DRIVER>
```

<h3>Arguments</h3>

`DRIVER`

:   Name of the driver to remove.

## sync

Install any missing drivers from the [drivers list](../concepts/drivers_list.md).

<h3>Usage</h3>

```sh
dbc sync
dbc sync --file dbc.toml
```

<h3>Options</h3>

`--file`

:   Path to the [drivers list](../concepts/drivers_list.md) file to sync. Defaults to `dbc.toml` in the current working directory.

`--level`

:   The configuration level to install drivers to (`user`, or `system`). See [Config](config.md).
