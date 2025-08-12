<!-- Copyright (c) 2025 Columnar Technologies.  All rights reserved. -->

# dbc

`dbc` is a command-line tool that makes installing and managing [ADBC](https://arrow.apache.org/adbc) drivers easy as A, B, C.

TODO: terminal screenshot or code showing dbc install?

## Features

- Install pre-built [ADBC](https://arrow.apache.org/adbc) drivers with a single command
- Manage numerous drivers without conflicts
- Connect to multiple distribution channels
- Install just for your user or system-wide
- Create reproducible environments with lockfiles
- Cross-platform: Runs on macOS, Linux, and Windows
- Installable with pip
- Works great in CI/CD environments

## Installation

`dbc` can be installed from [PyPI](https://pypi.org/project/dbc/) or [GitHub Releases](https://github.com/columnar-tech/dbc/releases/latest).

To install `dbc` from [PyPI](https://pypi.org/project/dbc/), create a virtual environment and install `dbc` into it:

```bash
python -m venv .venv
source .venv/bin/activate
pip install dbc
dbc # dbc is now in your $PATH
```

See our detailed [Installation](./getting_started/installation.md) guide for other installation options.
