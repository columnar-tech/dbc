# Contributing to dbc

Thanks for choosing to contribute to dbc. Please read the following sections for more information on contributing.

## Code of Conduct

By choosing to contribute to dbc, you agree to follow our [Code of Conduct](https://github.com/columnar-tech/.github/blob/main/CODE_OF_CONDUCT.md).

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

### C API and Rust crates

`./capi` is dbc's C API. cgo generates its header, `libdbc.h`, from the
preamble and `//export` functions in `capi/exports.go`, so that file is the
only place the API is declared. The Rust crates in `rust/` link a bundled
static build by default, or a system/custom library as described below.
Building libdbc needs a C compiler (cgo) as well as Go; using the Rust crates
also needs Rust. For bundled linking, build the archive for your platform
(`linux-x64`, `linux-arm64`, `darwin-x64`, `darwin-arm64`, or `windows-x64`)
into its platform crate, then run the tests:

```sh
CGO_ENABLED=1 go build -tags netgo,osusergo -buildmode=c-archive -o rust/platforms/linux-x64/native/libdbc.a ./capi
cargo test --manifest-path rust/Cargo.toml -p columnar-dbc
```

That build is for development only. Release archives are built by the `capi`
build in `.goreleaser.yaml`, which cross-compiles every target with Zig, and
the PR snapshot runs the Rust tests on those exact archives.

#### Linking a system libdbc

To test against a custom static build without staging a platform crate:

```sh
mkdir -p /tmp/dbc-local/lib
CGO_ENABLED=1 go build -tags netgo,osusergo -buildmode=c-archive -o /tmp/dbc-local/lib/libdbc.a ./capi
DBC_SYS_LIB_DIR=/tmp/dbc-local/lib cargo test --manifest-path rust/Cargo.toml -p columnar-dbc --no-default-features
```

Alternatively, install `libdbc-dev` (Debian/Ubuntu) or `libdbc-devel` (RPM),
along with the `pkg-config` executable. For example, use
`sudo apt install ./libdbc-dev_*.deb` for a downloaded Debian package.
These packages provide the static and shared libraries, header, and `dbc.pc`.
Then run:

```sh
cargo test --manifest-path rust/Cargo.toml -p columnar-dbc --no-default-features --features pkg-config
```

Set `PKG_CONFIG_PATH` to the directory containing `dbc.pc` if it is not in
the system search path. Disabling default features avoids downloading or
building the bundled platform crates. Source precedence is `DBC_SYS_LIB_DIR`,
then the `pkg-config` feature or presence of `DBC_SYS_USE_PKG_CONFIG`, then
the default `bundled` feature; environment overrides also work with default
features still enabled. With no source selected, the build fails.

System/custom linking defaults to static. Set `DBC_DYNAMIC` (any value,
including empty) for dynamic linking; bundled linking remains static.
To test a custom shared build on Linux:

```sh
CGO_ENABLED=1 go build -tags netgo,osusergo -buildmode=c-shared -ldflags '-extldflags "-Wl,-soname,libdbc.so"' -o /tmp/dbc-local/lib/libdbc.so ./capi
DBC_SYS_LIB_DIR=/tmp/dbc-local/lib DBC_DYNAMIC=1 LD_LIBRARY_PATH=/tmp/dbc-local/lib cargo test --manifest-path rust/Cargo.toml -p columnar-dbc --no-default-features
```

Dynamic linking requires `libdbc.so` on the runtime loader's search path;
the crate does not configure it. See the
[Rust crate README](rust/columnar-dbc/README.md#linking-a-system-libdbc)
for dependency configuration.

#### Regenerating Rust bindings

After changing anything in `capi/exports.go`, regenerate the Rust bindings
with bindgen-cli 0.73.2 (CI fails if they are stale):

```sh
cargo install bindgen-cli --version 0.73.2 --locked
python3 scripts/gen_rust_bindings.py rust/platforms/linux-x64/native/libdbc.h
```

## Debugging

Debugging dbc can be done similarly to any Golang project and editor setups like VSCode with the official Go extension should work out of the box.

To use [dlv](https://github.com/go-delve/delve) on the command line to debug a dbc subcommand, like `go run ./cmd/dbc install some_driver`, a bit of extra setup is required.
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
(dlv) b main.verifySignatureInStaging
(dlv) c
> [Breakpoint 1] main.verifySignatureInStaging() /Users/user/src/columnar-tech/dbc/cmd/dbc/install.go (hits goroutine(99):1 total:1) (PC: 0x105201f88)
```

When you're done, exiting the client should cause the server to exit automatically.

## Manual Testing

### Generating Test Driver Registry

Use `create.py` to generate a local driver registry with fake data for testing:

```sh
pixi run cdn-generate
```

then update `defaultURL` in drivers.go to:

```go
const defaultURL = "http://localhost:8000"
```

and run:

```sh
pixi run cdn-serve
```

## Commit Messages

We follow the [Conventional Commits](https://www.conventionalcommits.org) standard for commit messages. This includes titles for Pull Requests.
