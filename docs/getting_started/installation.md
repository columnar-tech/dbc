# Installation

`dbc` is distributed as a single, platform-native binary for easy installation.
You can install it using our automated installation script or manually using the latest release.

## Automated Install

Run the following in your terminal program of choice:

```sh
curl https://dbc.columnar.tech | sh
```

## Manual Install

First, download the archive for your operating system and CPU architecture.

| Operating System | Architecture | Link                                |
|------------------|--------------|-------------------------------------|
| Linux            | amd64        | <http://example.com/archive.tar.gz> |
|                  | arm64        | <http://example.com/archive.tar.gz> |
| macOS            | amd64        | <http://example.com/archive.tar.gz> |
|                  | arm64        | <http://example.com/archive.tar.gz> |
| Windows          | amd64        | <http://example.com/archive.tar.gz> |
|                  | arm64        | <http://example.com/archive.tar.gz> |

Then, in your terminal program of choice,

```sh
tar xzvf archive.tar.gz
# Now you can run dbc
./dbc --help
```

Note: You may wish to move the `dbc` binary to a location in your `PATH`.

## Other Methods

- winget
- Homebrew
- PyPi

## Uninstall

We might want commands to remove all drivers...
