<!-- Copyright (c) 2025 Columnar Technologies.  All rights reserved. -->

# dbc docs

dbc uses [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/) for docs.
Building the docs requires a Python installation.

## Setup

From the root of the repository, run:

```sh
python3 -m venv .venv
source .venv/bin/activate
pip install -r docs/requirements.txt
```

## Building

To build the docs website, run,

```sh
mkdocs build
```

The built docs website will be in `./site` and you can preview it by running this (or something similar):

```sh
python3 -m http.server -d site
```

## Developing

mkdocs has live preview+reload. To use it while developing, run:

```sh
mkdocs serve
```
