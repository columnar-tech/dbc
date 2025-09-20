<!-- Copyright (c) 2025 Columnar Technologies.  All rights reserved. -->

# Installation

dbc itself is installable on the most common platforms and from a variety of sources.

## Standalone Installer

We provide an automated command-line installer for users who prefer it.
Please continue reading for other installation methods.

The following commands will automatically install the latest version of dbc suitable for your system and place it in a standard location for you.

=== "macOS and Linux"

    To automatically install dbc, run:

    ```sh
    curl -LsSf https://dbc.columnar.tech/install.sh | sh
    ```

    If your system doesn't have `curl` you can also use `wget`:

    ```sh
    wget -q0- https://dbc.columnar.tech/install.sh | sh
    ```

    If you want to inspect the script before use, you can simply run:

    ```sh
    curl -LsSf https://dbc.columnar.tech/install.sh | less
    ```

=== "Windows"

    Use `irm` to download the script and execute it with `iex`:

    ```sh
    powershell -ExecutionPolicy ByPass -c "irm https://dbc.columnar.tech/install.ps1 | iex
    ```

    Changing the [execution policy](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_execution_policies?view=powershell-7.4#powershell-execution-policies) allows running a script from the internet.

    Of course, you can also inspect the script before use:

    ```sh
    powershell -c "irm https://dbc.columnar.tech/install.ps1 | more"
    ```

## PyPI

dbc is published as a package on [PyPI](https://pypi.org/project/dbc) for convenience.

To install dbc with [pipx](https://pipx.pypa.io/stable/installation/), run,

```sh
pipx install dbc
```

If you only want to run dbc to test it out, run,

```sh
pipx run dbc
```

### Virtual Environment

Installing dbc inside a virtual environment automatically handles installing dbc and adding it to your `$PATH`:

```sh
python -m venv .venv
source .venv/bin/activate
pip install dbc
```

## GitHub Releases

All dbc release artifacts are can be found at [GitHub Releases](https://github.com/columnar-tech/dbc/releases).
We always recommend installing dbc from the [latest release](https://github.com/columnar-tech/dbc/releases/latest).

## Windows Installer

A Windows MSI installer for x86_64 (i.e., x64, amd64) systems can be found as artifacts in our [GitHub Releases](https://github.com/columnar-tech/dbc/releases).
You can also download the latest installer using the following URL:

| Architecture | Link                                                    |
|--------------|---------------------------------------------------------|
| `x64`        | <https://dbc.columnar.tech/latest/dbc-latest-x64.msi>   |

## Docker

We publish [Docker](https://docker.io) images for each dbc release.

Run the latest version of dbc under Docker by running:

```sh
docker run --rm -it columnar/dbc:latest --help
```

### Available Images

The following distroless images are available for Linux-based `amd64` and `arm64` architectures:

- `columnar/dbc:latest`
- `columnar/dbc:{major}.{minor}.{patch}`, e.g. `columnar/dbc:0.0.1`
