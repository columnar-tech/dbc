# columnar-dbc

Rust bindings for [dbc](https://columnar.tech/dbc), the tool for finding,
installing, and verifying [ADBC](https://arrow.apache.org/adbc/) drivers.
By default, dbc itself is statically linked from a prebuilt library: there is
no `dbc` executable to install and no Go toolchain needed at build time.

```rust
use columnar_dbc::{list_installed, Client, InstallLocation};

let client = Client::new()?;
for d in client.search("postgres")?.drivers {
    println!("{} - {}", d.path, d.description);
}

// Into dbc's user-level location, like `dbc install -l user` ...
let manifest = client.install("postgresql", &InstallLocation::User)?;
println!("installed {} {} at {}", manifest.id, manifest.version, manifest.driver_path);

// ... or into an explicit directory.
let dir = InstallLocation::Directory("/opt/adbc/drivers".into());
client.install("postgresql", &dir)?;
assert_eq!(list_installed(&dir)?.len(), 1);
# Ok::<(), columnar_dbc::Error>(())
```

`install` verifies each driver's signature with Columnar's key and removes a
driver that fails. For drivers from a trusted private registry,
`install_with(name, &location, &InstallOptions::default().allow_unsigned(true))`
skips that, like the CLI's `--no-verify`. `InstallLocation::System` matches
`dbc install -l system` and usually needs elevated privileges. Calls block on
network and filesystem I/O; from async code, use `spawn_blocking`. `Client` is
`Send` but not `Sync`.

## Supported bundled targets

| Target | Requirement |
|---|---|
| `x86_64-unknown-linux-gnu`, `aarch64-unknown-linux-gnu` | glibc 2.28+ |
| `x86_64-apple-darwin`, `aarch64-apple-darwin` | macOS 12+ |
| `x86_64-pc-windows-msvc`, `x86_64-pc-windows-gnu` | Windows 10+ |

The default `bundled` feature supports these targets; other targets fail at
build time with a list of supported targets. Each target downloads one
`columnar-dbc-sys-<platform>` crate (~4 MB) containing the prebuilt library,
and links about 11 MB into your binary. System/custom linking is not limited
to this list, but requires a libdbc built for your target.

## Linking a system libdbc

Install the `libdbc-dev` package on Debian/Ubuntu or `libdbc-devel` on
RPM-based systems. These development packages contain `libdbc.a`,
`libdbc.so`, `libdbc.h`, and `dbc.pc`. For a downloaded Debian package:

```sh
sudo apt install ./libdbc-dev_*.deb
sudo apt install pkg-config
```

Disable default features and enable `pkg-config` in your dependency:

```toml
[dependencies]
columnar-dbc = { version = "0", default-features = false, features = ["pkg-config"] }
```

This mode discovers the `dbc` pkg-config module and does not download the
bundled platform crates. For a nonstandard installation prefix, set
`PKG_CONFIG_PATH` to the directory containing `dbc.pc`.

The library source is selected in this order:

1. `DBC_SYS_LIB_DIR` selects a custom library directory, without pkg-config.
2. The `pkg-config` feature or the presence of `DBC_SYS_USE_PKG_CONFIG`
   selects pkg-config.
3. The `bundled` feature selects the prebuilt platform library.
4. Without any of these, the build fails with instructions to select a source.

Both environment overrides work even when the default `bundled` feature is
enabled. Disable default features to avoid downloading/building unused
platform crates. `DBC_SYS_USE_PKG_CONFIG` is a presence flag: even an empty
value enables it.

System/custom linking is **static by default**. A custom directory must
contain `libdbc.a`; for example, `DBC_SYS_LIB_DIR=/opt/dbc/lib cargo build`.
Static pkg-config linking also selects `libdbc.a`, even under a system
library directory.
Static linking on MSVC targets is supported only for x86_64, including
system/custom libraries; other MSVC architectures fail at build time.

Set `DBC_DYNAMIC` to any value (including an empty string) to dynamically
link a system/custom library instead. It does not change bundled linking,
which is always static. Dynamic linking needs `libdbc.so` on the runtime
loader's search path on Linux; use a system library directory or set
`LD_LIBRARY_PATH`, for example:

```sh
DBC_SYS_LIB_DIR=/opt/dbc/lib DBC_DYNAMIC=1 cargo build
LD_LIBRARY_PATH=/opt/dbc/lib ./target/debug/my-app
```

The crate does not configure the runtime loader path. Other platforms need
their corresponding shared library and loader-path configuration.

## Private registries

`Client::new()` uses credentials stored by `dbc auth login`. To inject a
token at runtime instead, use `Client::with_config` with a `Credential`.
