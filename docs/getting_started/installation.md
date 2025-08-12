<!-- Copyright (c) 2025 Columnar Technologies.  All rights reserved. -->

# Installation

dbc can be installed from [PyPI](#from-pypi) or [GitHub Releases](#from-github-releases).

## From PyPI

We recommend installing dbc using the popular [pipx](https://pipx.pypa.io/stable/installation/) tool because it automatically puts dbc in your `$PATH`.

To install dbc with [pipx](https://pipx.pypa.io/stable/installation/), run,

```sh
$ pipx install dbc
```

If you only want to run dbc to test it out, run,

```sh
$ pipx run dbc
```

### Using a Virtual Environment

Installing dbc inside a virtual environment automatically handles installing dbc and adding it to your `$PATH`:

```sh
$ python -m venv .venv
$ source .venv/bin/activate
$ pip install dbc
```

## From GitHub Releases

dbc is also published using [GitHub Releases](https://github.com/columnar-tech/dbc/releases/latest).
We always recommend installing dbc from the [latest release](https://github.com/columnar-tech/dbc/releases/latest).

To do that, first download the archive for your operating system and CPU architecture.

| Operating System | Architecture | Link                                |
|------------------|--------------|-------------------------------------|
| Linux            | `amd64`        | <http://example.com/archive.tar.gz> |
|                  | `aarch64`        | <http://example.com/archive.tar.gz> |
| macOS            | `amd64`        | <http://example.com/archive.tar.gz> |
|                  | `aarch64`        | <http://example.com/archive.tar.gz> |
| Windows          | `amd64`        | <http://example.com/archive.tar.gz> |
|                  | `aarch64`        | <http://example.com/archive.tar.gz> |

Then, in your terminal program of choice, decompress the `.tar.gz`:

```sh
tar xzvf archive.tar.gz
# Now you can run dbc
./dbc --help
```

Most users will choose to move dbc to a location already in their `$PATH` or create a new place for dbc and add that to their `$PATH`.
