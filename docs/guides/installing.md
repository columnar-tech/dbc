# Installing Drivers

## Outline

We want a longer-form guide for the basic workflows related to installing drivers.

1. Install a single driver (pick one), show where it goes, what it installs
2. Show installing with version constraints
3. Show how installing can be updating (i.e., install x==1.0.0, install x>1.0.0)
4. Cover behavior driven by environment variables (`ADBC_DRIVER_PATH`, `VIRTUAL_ENV`, `CONDA_PREFIX`)
5. Show how `--level` works
6. Cover uninstalling?

## Content

Once you've [installed dbc](../getting_started/installation.md), the first thing you'll probably want to do is install a driver.
But before you can install a driver, you need to know what drivers are available.
For that, you can use `dbc search` which will show all available drivers:

```console
$ dbc search
...output...
```

The short names in lowercase on the left of the output are the names you need to pass to `dbc install`.

To install a specific driver, such as `mysql`, run:

```console
$ dbc install mysql
```

### Version Constraints

By default, dbc installs the latest version of the package you specify.
To specify that a specific version should be installed, you can pass a version constraint with the name:

```console
$ dbc install "mysql@0.1.0"
```
The syntax for specifiying a version may be familiar to you if you've used other package managers.

!!! note
    dbc uses the [github.com/Masterminds/semver/v3](https://pkg.go.dev/github.com/Masterminds/semver/v3#section-readme) package whose README has a good overview of the syntax it allows. In short, you can use `=`, `!=`, `>`, `<`, `>=`, `<=`, `~`, `^`, ranges like `1.2 - 1.4.5`, and wildcards (`x`, `X`, or `*`).

### Updating a Driver

dbc doesn't offer a specific "update" or "upgrade" command so `dbc install` can do essentially the same thing.

For example, if you installed `mysql@0.1.0` and then version 0.2.0 is released, re-running `dbc install mysql` will upgrade your installed version.

### Install Levels

By default, dbc installs drivers to the standard user-level ADBC driver path suitable for your system:

- macOS: `~/Library/Application Support/ADBC/Drivers`
- Linux: `~/.config/adbc/drivers`
- Windows: `%LOCAL_APPDATA%\ADBC\Drivers`

Numerous dbc subcommands, including `install`, accept an optional `--level` flag which can used to install drivers system-wide. For example:

```console
dbc install --level system mysql
```

Where this installs depends on your operating system:

- macOS: `/Library/Application Support/ADBC/Drivers`
- Linux: `/etc/adbc/drivers`
- Windows: `C:\Program Files\ADBC\Drivers`

!!! note

    https://arrow.apache.org/adbc/main/format/driver_manifests.html#manifest-location-and-discovery

[Config](../reference/config.md)

### Environment Variables
