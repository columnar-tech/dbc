<!--
Copyright 2025 Columnar Technologies Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# Installation

dbc is installable on the most common platforms and from a variety of sources.

## Standalone Installer

We provide an automated command-line installer for users who prefer it.
Please continue reading for other installation methods.

The following commands will automatically install the latest version of dbc suitable for your system and place it in a standard location for you.

=== "macOS and Linux"

    To automatically install dbc, run:

    ```console
    $ curl -LsSf https://dbc.columnar.tech/install.sh | sh
    ```

    If your system doesn't have `curl` you can also use `wget`:

    ```console
    $ wget -q0- https://dbc.columnar.tech/install.sh | sh
    ```

    If you want to inspect the script before use, you can simply run:

    ```console
    $ curl -LsSf https://dbc.columnar.tech/install.sh | less
    ```

=== "Windows"

    Use `irm` to download the script and execute it with `iex`:

    ```console
    $ powershell -ExecutionPolicy ByPass -c "irm https://dbc.columnar.tech/install.ps1 | iex"
    ```

    Changing the [execution policy](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_execution_policies?view=powershell-7.4#powershell-execution-policies) allows running a script from the internet.

    Of course, you can also inspect the script before use:

    ```console
    $ powershell -c "irm https://dbc.columnar.tech/install.ps1 | more"
    ```

## PyPI

dbc is published on [PyPI](https://pypi.org/) as [dbc](https://pypi.org/project/dbc/) for convenience. The package contains the appropriate dbc executable for your system and makes it available to various tools in the Python ecosystem.

### uv

To run dbc with [uv](https://docs.astral.sh/uv/), you can run any of the following:

```console
$ uv run --with "dbc" dbc
$ uv tool run dbc
$ uvx dbc
```

To install dbc as a uv tool, run:

```sh
$ uv tool install dbc
$ # Now run dbc with
$ dbc
```

To learn more about `uv tool`, see uv's [Tools](https://docs.astral.sh/uv/concepts/tools/) documentation.

### pipx

To install dbc with [pipx](https://pipx.pypa.io/stable/installation/), run,

```console
$ pipx install dbc
```

If you only want to run dbc to test it out, run,

```console
$ pipx run dbc
```

### Virtual Environment

Installing dbc inside a virtual environment automatically handles installing dbc and adding it to your `$PATH`:

```console
$ python -m venv .venv
$ source .venv/bin/activate
$ pip install dbc
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

```console
$ docker run --rm -it columnar/dbc:latest --help
```

### Available Images

The following distroless images are available for Linux-based `amd64` and `arm64` architectures:

- `columnar/dbc:latest`
- `columnar/dbc:{major}.{minor}.{patch}`, e.g. `columnar/dbc:0.0.1`
