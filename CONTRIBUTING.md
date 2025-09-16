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

## Debugging

To use the [Delve](https://github.com/go-delve/delve) debugger to debug dbc, some special steps are required.
This is because dbc uses [bubbletea](https://github.com/charmbracelet/bubbletea/) which takes control of stdin/stdout.

The trick is to start `dlv` in headless mode with any command line arguments we need and then to connect and control it with a separate dlv client.

As an example, if you want to debug the specific invocation of `dbc install some_driver`, start dlv like this:

```console
$ dlv debug ./cmd/dbc --headless --listen=:2345 --api-version=2 -- install some_driver
API server listening at: [::]:2345
2025-09-16T10:59:24-07:00 warn layer=rpc Listening for remote connections (connections are not authenticated nor encrypted)
debugserver-@(#)PROGRAM:LLDB  PROJECT:lldb-1700.0.9.502
 for arm64.
Got a connection, launched process /Users/user/src/columnar-tech/dbc/__debug_bin464674121 (pid = 96049).
```

Then in another shell, run `dlv connect` and debug with dlv as you normally would. In this example, I set a breakpoint and continue:

```console
$ dlv connect 127.0.0.1:2345
Type 'help' for list of commands.
(dlv) b install.go:58
(dlv) c
> [Breakpoint 1] main.verifySignature() /Users/user/src/columnar-tech/dbc/cmd/dbc/install.go:58 (hits goroutine(99):1 total:1) (PC: 0x105201f88)
```

When you're done, exiting the client should cause the server to exit automatically.

## Commit Messages

We follow the [Conventional Commits](https://www.conventionalcommits.org) standard for commit messages. This includes titles for Pull Requests.
