dbc is a command-line tool for installing and managing [ADBC](https://arrow.apache.org/adbc) drivers.
This is the official set of Docker images for dbc.

## Usage

```sh
docker run -it --rm columnar/dbc:latest dbc --help
```

## Image tags

The following distroless images are available for Linux-based `amd64` and `arm64` architectures:

- `columnar/dbc:latest`
- `columnar/dbc:{major}.{minor}.{patch}`, e.g. `columnar/dbc:0.0.1`
