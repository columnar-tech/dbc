# Contributing to dbc

Thanks for choosing to contribute to dbc. Please read the following sections for more information on contributing.

## Reporting Issues

Please file bug reports, feature requests, or questions as new [Issues](https://github.com/columnar-tech/dbc/issues/new/choose). For bug reports, please be sure to provide as much information as you think may be required for a maintainer to reproduce your issue. This will typically involve your operating system, Go version, dbc version, and a set of commands we can run to reproduce your issue.

## Creating Pull Requests

[Filing an issue](https://github.com/columnar-tech/dbc/issues/new/choose) before creating is encouraged. Please reference an Isuse in your Pull Request body.

## Setting Up Your Developer Environment

dbc has minimal requirements for building and testing, requiring only only an installation of [Go](https://go.dev/doc/install).

dbc can be built by running,

```sh
go build -o dbc ./cmd/dbc
```

and you can run the tests by running,

```sh
go test -v ./...
```

## Commit Messages

We follow the [Conventional Commits](https://www.conventionalcommits.org) standard for commit messages. This includes titles for Pull Requests.
