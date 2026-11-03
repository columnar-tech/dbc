<!--
Copyright 2026 Columnar Technologies Inc.

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

To run dbc with [uv](https://docs.astral.sh/uv/), you can run either of the following:

```console
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

## Linux Package Repositories (deb/rpm)

dbc's Debian (`.deb`) and RPM (`.rpm`) packages are published to the
[Cloudsmith](https://cloudsmith.com) repository
[`columnar/dbc`](https://cloudsmith.io/~columnar/repos/dbc/packages/) for every
stable release. The same packages are attached to each
[GitHub release](https://github.com/columnar-tech/dbc/releases) if you prefer
to download and install them directly.

The repository provides:

| Package | Format | Contents |
| --- | --- | --- |
| `dbc` | deb, rpm | The dbc command-line tool |
| `libdbc-dev` | deb | dbc's C library (`libdbc.a`, `libdbc.so`), header, and `dbc.pc` pkg-config file, e.g. for the [`columnar-dbc`](https://crates.io/crates/columnar-dbc) Rust crate |
| `libdbc-devel` | rpm | The same development files for RPM-based distributions |

The packages support x86_64 and aarch64 Linux with glibc 2.28 or newer, on any
Debian- or RPM-based distribution.

=== "Debian/Ubuntu (apt)"

    Add the repository with Cloudsmith's setup script:

    ```console
    $ curl -1sLf 'https://dl.cloudsmith.io/public/columnar/dbc/setup.deb.sh' | sudo -E bash
    ```

    Or add it manually:

    ```console
    $ curl -1sLf 'https://dl.cloudsmith.io/public/columnar/dbc/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/columnar-dbc-archive-keyring.gpg
    $ echo 'deb [signed-by=/usr/share/keyrings/columnar-dbc-archive-keyring.gpg] https://dl.cloudsmith.io/public/columnar/dbc/deb/any-distro any-version main' | sudo tee /etc/apt/sources.list.d/columnar-dbc.list
    $ sudo apt update
    ```

    Then install dbc, the development package, or both:

    ```console
    $ sudo apt install dbc
    $ sudo apt install libdbc-dev pkg-config
    ```

=== "Fedora/RHEL (dnf)"

    Add the repository with Cloudsmith's setup script:

    ```console
    $ curl -1sLf 'https://dl.cloudsmith.io/public/columnar/dbc/setup.rpm.sh' | sudo -E bash
    ```

    Or add it manually:

    ```console
    $ curl -1sLf 'https://dl.cloudsmith.io/public/columnar/dbc/config.rpm.txt?distro=any-distro&codename=any-version' | sudo tee /etc/yum.repos.d/columnar-dbc.repo
    ```

    Then install dbc, the development package, or both:

    ```console
    $ sudo dnf install dbc
    $ sudo dnf install libdbc-devel pkgconf-pkg-config
    ```

To install a downloaded package from GitHub Releases instead, pass the file
to the package manager, e.g. `sudo apt install ./libdbc-dev_*_amd64.deb` or
`sudo dnf install ./libdbc-devel-*.x86_64.rpm`.

## libdbc Development Libraries (macOS and Windows)

dbc's C library, used for example by the
[`columnar-dbc`](https://crates.io/crates/columnar-dbc) Rust crate, is also
released as a static library for macOS and Windows. Each
`libdbc-dev_<version>_<os>_<arch>` archive on
[GitHub Releases](https://github.com/columnar-tech/dbc/releases) contains
`libdbc.a` (`libdbc.lib` on Windows), the `libdbc.h` header, and a relocatable
`dbc.pc` pkg-config file.

=== "macOS (Homebrew)"

    The `libdbc` formula in the `columnar-tech/tap` tap supports x86_64 and
    arm64 on macOS 12 or newer, and installs the library, header, and `dbc.pc`
    into Homebrew's prefix:

    ```console
    $ brew install pkgconf columnar-tech/tap/libdbc
    $ pkg-config --modversion dbc
    ```

    To use a downloaded archive instead, unpack it and set `PKG_CONFIG_PATH` to
    the unpacked directory.

=== "Windows"

    The Windows x64 zip is published to the Cloudsmith repository as the raw
    package [`libdbc-dev-windows-x64`](https://cloudsmith.io/~columnar/repos/dbc/packages/)
    for every stable release, and attached to each GitHub release:

    ```powershell
    $v = "<version>"
    Invoke-WebRequest -OutFile libdbc-dev.zip "https://dl.cloudsmith.io/public/columnar/dbc/raw/names/libdbc-dev-windows-x64/versions/$v/libdbc-dev_${v}_windows_amd64.zip"
    Expand-Archive libdbc-dev.zip -DestinationPath .
    ```

    For the Rust crate, set `DBC_SYS_LIB_DIR` to the unpacked
    `libdbc-dev_<version>_windows_amd64` directory.

## GitHub Releases

All dbc release artifacts are can be found at [GitHub Releases](https://github.com/columnar-tech/dbc/releases).
We always recommend installing dbc from the [latest release](https://github.com/columnar-tech/dbc/releases/latest).

## Windows Installer

A Windows MSI installer for x86_64 (i.e., x64, amd64) systems can be found as artifacts in our [GitHub Releases](https://github.com/columnar-tech/dbc/releases).
You can also download the latest installer using the following URL:

| Architecture | Link                                                    |
|--------------|---------------------------------------------------------|
| `x64`        | <https://dbc.columnar.tech/latest/dbc-latest-x64.msi>   |

!!! note

    Starting with dbc 0.2.0, the MSI installer only supports per-user installs. The 0.1.0 MSI installed system-wide.

## WinGet

On Windows, you can install dbc using [WinGet](https://learn.microsoft.com/en-us/windows/package-manager/winget/):

```console
$ winget install Columnar.dbc
```

!!! note

    If you installed dbc 0.1.0 with WinGet, it was installed system-wide and `winget upgrade dbc` cannot upgrade it. Uninstall the system-level package first, then install again:

    ```console
    $ winget uninstall --id Columnar.dbc
    $ winget install Columnar.dbc
    ```

## Docker

We publish [Docker](https://docker.io) images for each dbc release.

Run the latest version of dbc under Docker by running:

```console
$ docker run --rm -it columnar/dbc:latest --help
```

### Available Images

The following distroless images are available for Linux-based `amd64` and `arm64` architectures:

- `columnar/dbc:latest`
- `columnar/dbc:{major}`, e.g. `columnar/dbc:1`
- `columnar/dbc:{major}.{minor}`, e.g. `columnar/dbc:0.1`
- `columnar/dbc:{major}.{minor}.{patch}`, e.g. `columnar/dbc:0.0.1`

## Homebrew

You can install dbc from our Homebrew tap by running:

```console
$ brew install columnar-tech/tap/dbc
```

## Shell Completions

dbc can generate shell completions for a number of common shells.

!!! note

    If you aren't sure what shell you're running, you can run the following command in your terminal:

    ```console
    $ echo $SHELL
    ```

=== "Bash"

    ```console
    $ echo 'eval "$(dbc completion bash)"' >> ~/.bashrc
    ```

=== "Zsh"

    ```console
    $ echo 'eval "$(dbc completion zsh)"' >> ~/.zshrc
    ```

=== "fish"

    ```console
    $ dbc completion fish > ~/.config/fish/completions/dbc.fish
    ```

!!! info

    You can use the `dbc completion` subcommand to print extended instructions for your shell, including how to enable your shell's completion mechanism. For example, to print setup instructions for Bash, run `dbc completion bash --help`.

## Uninstallation

To remove dbc from your system, run the uninstall command corresponding to your installation method.

=== "Linux/macOS shell"

    ```console
    $ rm $HOME/.local/bin/dbc
    ```

=== "Windows shell"

    ```console
    $ powershell.exe -c "rm $HOME/.local/bin/dbc.exe"
    ```

=== "Windows MSI"

    Go to **Settings** > **Apps** > **Installed apps** (or **Control Panel** > **Programs and Features**), select dbc, and click **Uninstall**.

=== "WinGet"

    ```console
    $ winget uninstall dbc
    ```

=== "uv"

    ```console
    $ uv tool uninstall dbc
    ```

=== "pipx"

    ```console
    $ pipx uninstall dbc
    ```

=== "pip"

    ```console
    $ pip uninstall dbc
    ```

=== "Homebrew"

    ```console
    $ brew uninstall --cask dbc
    ```

!!! note

    Uninstalling dbc does not remove any drivers you've installed with either `dbc install` or `dbc sync`. To remove drivers, run [`dbc uninstall`](../reference/cli.md#uninstall) on each installed driver prior to uninstalling dbc.
