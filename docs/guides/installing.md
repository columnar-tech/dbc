# Installing Drivers

Once you've [installed dbc](../getting_started/installation.md), the first thing you'll probably want to do is install a driver.
But before you can install a driver, you need to know what drivers are available.

## Finding a Driver

You can use `dbc search` which will show all available drivers:

```console
$ dbc search
...output...
```

The short names in lowercase on the left of the output are the names you need to pass to `dbc install`.

## Installing a Driver

To install a specific driver, such as `mysql`, run:

```console
$ dbc install mysql
```

## Version Constraints

By default, dbc installs the latest version of the package you specify.
To specify that a specific version should be installed, you can pass a version constraint with the name:

```console
$ dbc install "mysql@0.1.0"
```
The syntax for specifiying a version may be familiar to you if you've used other package managers.

!!! note
    dbc uses the [github.com/Masterminds/semver/v3](https://pkg.go.dev/github.com/Masterminds/semver/v3#section-readme) package whose README has a good overview of the syntax it allows. In short, you can use `=`, `!=`, `>`, `<`, `>=`, `<=`, `~`, `^`, ranges like `1.2 - 1.4.5`, and wildcards (`x`, `X`, or `*`).

## Updating a Driver

dbc doesn't offer a specific "update" or "upgrade" command so `dbc install` can do essentially the same thing.

For example, if you installed `mysql@0.1.0` and then version 0.2.0 is released, re-running `dbc install mysql` will upgrade your installed version.

## Installing System Wide

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

TODO: Link to [Config](../reference/config.md)

## `ADBC_DRIVER_PATH`

For complete control over where dbc installs drivers, set the `ADBC_DRIVER_PATH` environment to a path where you want to install drivers.

```console
mkdir "$HOME/drivers"
export ADBC_DRIVER_PATH="$HOME/drivers"
dbc install mysql

[✓] searching
[✓] downloading
[✓] installing
[✓] verifying signature

Installed mysql 0.1.0 to /home/user/drivers
```

!!! note

    be aware that you have to set up your driver manager to match

## Python Support

By default, dbc automatically detects whether you've activated a Python [virtual environment](https://docs.python.org/3/tutorial/venv.html) and will install (and uninstall) drivers from the environment rather than the user or system-level paths.

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

Installed mysql 0.1.0 to /Users/bryce/tmp/my-adbc-project/.venv/etc/adbc/drivers
```

## Conda Support

By default, dbc automatically detects whether you've activated a [Conda environment](https://docs.conda.io/projects/conda/en/latest/user-guide/concepts/environments.html) and will install (and uninstall) drivers from the environment rather than the user or system-level paths.

```console
~/tmp/my-adbc-project
.venv $ conda create -n my-adbc-project
Retrieving notices: done
Channels:
 - conda-forge
Platform: osx-arm64
Collecting package metadata (repodata.json): done
Solving environment: done


==> WARNING: A newer version of conda exists. <==
    current version: 25.5.1
    latest version: 25.7.0

Please update conda by running

    $ conda update -n base -c conda-forge conda



## Package Plan ##

  environment location: /opt/homebrew/Caskroom/miniforge/base/envs/my-adbc-project



Proceed ([y]/n)? y


Downloading and Extracting Packages:

Preparing transaction: done
Verifying transaction: done
Executing transaction: done
#
# To activate this environment, use
#
#     $ conda activate my-adbc-project
#
# To deactivate an active environment, use
#
#     $ conda deactivate


~/tmp/my-adbc-project 6s
.venv $ conda activate my-adbc-project

~/tmp/my-adbc-project
.venv $ deactivate                                                                                                                       (my-adbc-project)

~/tmp/my-adbc-project
my-adbc-project $ dbc install mysql                                                                                                      (my-adbc-project)
[✓] searching
[✓] downloading
[✓] installing
[✓] verifying signature

Installed mysql 0.1.0 to /opt/homebrew/Caskroom/miniforge/base/envs/my-adbc-project/etc/adbc/drivers
```

## Uninstalling Drivers

You can uninstall a driver with the `dbc uninstall` subcommand.

```console
dbc uninstall mysql

(TODO) Actual uninstall message here.
```

Since it's possible to install the same driver to multiple locations, dbc will only uninstall the first driver it finds.
dbc will search in the following order:

- Environment
    - `ADBC_DRIVER_PATH`
    - `VIRTUAL_ENV`
    - `CONDA_PREFIX`
- User
- System

Make sure to say that when you have drivers installed at different levels, only the top-most level will be uninstalled.
