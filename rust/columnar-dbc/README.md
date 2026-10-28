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

## Registry configuration

`Client::new()` and `ClientConfig::default()` honor the user's global dbc
`config.toml`, just like the CLI. This is a change from earlier bindings,
which ignored that file. If it is missing, dbc uses its built-in registries.
An unreadable or invalid global file is a construction error, not a warning.
The locations are:

| Platform | Global registry configuration |
|---|---|
| Linux | `$XDG_CONFIG_HOME/columnar/dbc/config.toml`, or `~/.config/columnar/dbc/config.toml` |
| macOS | `~/Library/Application Support/Columnar/dbc/config.toml` |
| Windows | `%APPDATA%\Columnar\dbc\config.toml` |

Use the non-exhaustive `ClientConfig`'s builder methods to choose options:

```rust
use columnar_dbc::{Client, ClientConfig, CredentialStore, Registry, RegistryConfig};

let config = ClientConfig::default()
    .registry_config(RegistryConfig::None)
    .credential_store(CredentialStore::None)
    .registries(vec![
        Registry::new("https://registry.example.com").name("company"),
        Registry::new("https://another.example.com"),
    ]);
let client = Client::with_config(&config)?;
# Ok::<(), columnar_dbc::Error>(())
```

Registry precedence is explicit:

- `base_url(url)` selects one registry. A nonempty `registries(vec![...])`
  selects that exact list, in order. These options are mutually exclusive:
  setting both is an error. Invalid registry URLs are errors.
- Either explicit option bypasses global and project configuration
  entirely: files are not read, even if missing or malformed.
- Otherwise, `registry_config(RegistryConfig::Default)` loads the global
  file. `RegistryConfig::None` skips only that file; it does not disable
  an explicitly selected project file or dbc's built-in registries.
- `project_file(path)` reads `registries` and `replace_defaults` from that
  project's `dbc.toml`. Project files are never discovered automatically.
  A missing, unreadable, or invalid selected file is an error.
- Project registries come first, then global registries, then built-in
  registries, with duplicate URLs removed. A project's `replace_defaults`
  value overrides the global value; omitting it inherits the global value.
  `true` removes the built-in registries, not the global entries. A
  configuration that leaves no registries is an error.

An empty `registries` vector is the default, not a request for zero
registries. To load a project's configuration without the global file:

```rust
use columnar_dbc::{Client, ClientConfig, RegistryConfig};

let config = ClientConfig::default()
    .registry_config(RegistryConfig::None)
    .project_file("dbc.toml");
let client = Client::with_config(&config)?;
# Ok::<(), columnar_dbc::Error>(())
```

## Credentials

`CredentialStore::Default` uses credentials saved by `dbc auth login` or
the free `login(&Credential) -> Result<()>` function. `login` stores a token
credential, replacing any entry for the same registry host; it does not
perform browser-based or interactive authentication. Optional
`refresh_token` and `client_id` values are saved too.

To inject a credential into one client instead of storing it:

```rust
use columnar_dbc::{Client, ClientConfig, Credential};

let credential = Credential {
    registry_url: "https://registry.example.com".into(),
    auth_uri: "https://auth.example.com/oauth/token".into(),
    token: Some(std::env::var("DBC_REGISTRY_TOKEN")?),
    ..Default::default()
};
let config = ClientConfig::default()
    .base_url("https://registry.example.com")
    .credential(credential);
let client = Client::with_config(&config)?;
# Ok::<(), Box<dyn std::error::Error>>(())
```

An injected credential replaces the stored credential **only for its
registry host**. Other hosts fall back to the store by default. With
`credential_store(CredentialStore::None)`, stored credentials are never
used to authenticate; refreshing an injected credential can still update
dbc's credential/license files.

`logout(registry_url: &str) -> Result<()>` removes the credential for the
URL's host, independent of its path. It returns an error if that host has
no stored credential. `login` and `logout` operate on the shared persistent
store regardless of any client's `CredentialStore` setting.
Cached lookups may miss external store changes until the process restarts,
even if a new `Client` is created.

## Licenses

```rust,no_run
use columnar_dbc::{install_license, license_path};
use std::path::Path;

let destination = license_path()?;
println!("license destination: {}", destination.display());
install_license(Path::new("/path/to/columnar.lic"), false)?;
# Ok::<(), columnar_dbc::Error>(())
```

`license_path() -> Result<PathBuf>` returns the `columnar.lic` path beside
dbc's credential store, even if no license exists. It does not install a
license. `install_license(src_path: &Path, force: bool) -> Result<()>`
copies the source there. With `force = false`, the source must be named
`columnar.lic` and an existing license is not overwritten. `force = true`
allows another source filename and replaces an existing license. Filename
and overwrite errors refer to `force`, not the CLI's `--force` flag.

The credential and license directory is `$XDG_DATA_HOME/dbc/credentials`
when `XDG_DATA_HOME` is set. Otherwise it is
`~/.local/share/dbc/credentials` on Linux,
`~/Library/Application Support/Columnar/dbc/credentials` on macOS, or
`%LOCALAPPDATA%\dbc\credentials` on Windows.

The ordinary Rust roundtrip tests spawn child processes with isolated user
configuration and data directories before the Go runtime initializes.
They never write the user's credential store or license. The separate
user/system driver-level tests still require `DBC_TEST_LEVEL_USER` or
`DBC_TEST_LEVEL_SYSTEM` opt-in because they exercise real install locations.
