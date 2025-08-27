# dbc

This is a Python packaging of [dbc](https://github.com/columnar-tech/dbc).
[dbc](https://github.com/columnar-tech/dbc) is a command line too for installing [ADBC](https://arrow.apache.org/adbc) drivers.

## Installation

The package contains a [dbc](https://github.com/columnar-tech/dbc) executable which, when you install this package, will be installed to a location that may already be in your `PATH`.

### Virtual Environment

Using a virtual environment may be the simplest because `PATH` will be managed automatically (i.e., `dbc` will be immediately available).

Create and activate a virtual environment and then install dbc:

```sh
python -m venv .venv
source .venv/bin/activate
pip install dbc
```

Then run dbc,

```sh
dbc --help
```

### pipx

You may prefer [pipx](https://pipx.pypa.io/stable/) which handles the virtual environment for you:

```sh
pipx install dbc
```

Note: You may have to run `pipx ensurepath` to set up your `PATH` propertly before `dbc` will be runnable.

Then run `dbc`:

```sh
dbc --help
```
