<!-- Copyright (c) 2025 Columnar Technologies.  All rights reserved. -->

# Using the Drivers List

dbc can create and manage lists of drivers using a [drivers list](../concepts/drivers_list.md) file.
By default, a drivers list file has the name `dbc.toml`.

!!! note

    This functionality is similar to files from other tools such as Python's [`requirements.txt`](https://pip.pypa.io/en/stable/reference/requirements-file-format/).

The drivers list file is ideal for checking into version control alongside your project and is useful for recording not only which drivers your project needs but also the specific versions of each.

## Creating a Drivers List

Create a drivers list file with `dbc init`:

```console
$ dbc init
$ ls
dbc.toml
$ cat dbc.toml
# dbc driver list

[drivers]

```

The drivers list file uses the [TOML](https://toml.io) format and contains a TOML table of drivers.

## Adding a Driver

While you can edit `dbc.toml` manually, dbc has subcommands for working with the list.
To add a driver to the list, use `dbc add`:

```console
$ dbc add mysql
added mysql to driver list
use `dbc sync` to install the drivers in the list
$ cat dbc.toml
# dbc driver list
[drivers]
[drivers.mysql]
```

The `add` command automatically checks that a driver matching the pattern exists in the driver index.

!!! note

    `dbc add` accepts the same syntax for driver names and versions as `dbc install`. See the [Installing Drivers](installing.md).

If you read the above output, you'll notice that it's telling you to run `dbc sync` to install the driver(s) in the list. This is because `dbc add` only modifies the drivers list file and we need to use `dbc sync` to actually install the driver we just added.

## Synchronizing

Use `dbc sync` to ensure that all the drivers in the drivers list are installed. 

```console
$ dbc sync
...
```

TODO: Add output from dbc sync once https://github.com/columnar-tech/dbc/issues/32 is fixed.

## Version Constraints

Each driver in the drivers list can optionally include a version constraint which dbc will respect when you run `dbc sync`. You can add a driver to the list with the same syntax as you used for `dbc install`, see [Installing Drivers](installing.md).

```console
$ dbc add "mysql@0.1.0"
... # TODO once this works
$ cat dbc.toml
# dbc driver list
[drivers]
mysql = { "version": "0.1.0" }
```

## Removing Drivers

Drivers can be removed from the drivers list with the `dbc remove` command:

```console
$ dbc remove mysql
removed 'mysql' from driver list
```
