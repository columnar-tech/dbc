# columnar-dbc

Rust bindings for [dbc](https://columnar.tech/dbc), the tool for finding,
installing, and verifying [ADBC](https://arrow.apache.org/adbc/) drivers.
dbc itself is linked as a library: by default from an installed libdbc found
via pkg-config, or from a prebuilt static library with the `bundled` feature.
There is no `dbc` executable to install and no Go toolchain needed at build
time.

```rust,no_run
use columnar_dbc::{list_installed, Client, InstallLocation};

// First create a Client
let client = Client::new()?;

// Search for a driver using a pattern
for d in client.search("postgres")?.drivers {
    println!("{} - {}", d.path, d.description);
}

// Install a driver where `dbc install` would without -l: ADBC_DRIVER_PATH,
// $VIRTUAL_ENV, or $CONDA_PREFIX if set, otherwise the user level.
let manifest = client.install("postgresql")?;
println!("installed {} {} at {}", manifest.id, manifest.version, manifest.driver_path);

// You can also install the driver to a specific level or directory.
let dir = InstallLocation::Directory("/opt/adbc/drivers".into());
client.install_at("postgresql", &dir)?;
assert_eq!(list_installed(&dir)?.len(), 1);
# Ok::<(), columnar_dbc::Error>(())
```

`install` verifies each driver's signature with Columnar's public signing key
and cleans up a driver that fails verification. For drivers from a trusted
private registry,
`install_with(name, &location, &InstallOptions::default().allow_unsigned(true))`
skips that, like the CLI's `--no-verify`. `InstallLocation::User` and
`InstallLocation::System` match `dbc install -l user|system`; the system level
usually needs elevated privileges. dbc reads `ADBC_DRIVER_PATH`,
`VIRTUAL_ENV`, and `CONDA_PREFIX` when the process starts, so changing them
later from Rust has no effect; use `InstallLocation::Directory` instead.
Calls block on network and filesystem I/O; from async code, use
`spawn_blocking`. `Client` is `Send` but not `Sync`.

## Linking libdbc

By default, the crate links an installed libdbc located through pkg-config.
On Linux, install the `libdbc-dev` (Debian/Ubuntu) or `libdbc-devel`
(Fedora/RHEL and other RPM-based) development package, which provides
`libdbc.a`, `libdbc.so`, `libdbc.h`, and the `dbc.pc` pkg-config file. Also
install the `pkg-config` executable (`pkg-config` on Debian/Ubuntu,
`pkgconf-pkg-config` on Fedora/RHEL), which is a separate package. The packages
support x86_64 and aarch64 Linux with glibc 2.28+. They can be installed either
way:

- **From the package repository.** Every stable release is published to the
  Cloudsmith-hosted `columnar/dbc` apt/dnf repository
  (<https://cloudsmith.io/~columnar/repos/dbc/packages/>). Add the repository
  once, and the package manager handles upgrades.
- **From GitHub Releases.** Download the package for your architecture from
  <https://github.com/columnar-tech/dbc/releases> and install the file
  directly.

Debian/Ubuntu:

```sh
# Add the repository (or see the manual steps linked below)
curl -1sLf 'https://dl.cloudsmith.io/public/columnar/dbc/setup.deb.sh' | sudo -E bash
sudo apt install libdbc-dev pkg-config

# Or, with a package downloaded from GitHub Releases
sudo apt install ./libdbc-dev_*_amd64.deb pkg-config
```

Fedora/RHEL:

```sh
# Add the repository (or see the manual steps linked below)
curl -1sLf 'https://dl.cloudsmith.io/public/columnar/dbc/setup.rpm.sh' | sudo -E bash
sudo dnf install libdbc-devel pkgconf-pkg-config

# Or, with a package downloaded from GitHub Releases
sudo dnf install ./libdbc-devel-*.x86_64.rpm pkgconf-pkg-config
```

macOS (x86_64 and arm64, macOS 12+), with the `libdbc` Homebrew formula from
the `columnar-tech/tap` tap, which installs `libdbc.a`, `libdbc.h`, and
`dbc.pc`:

```sh
brew install pkgconf columnar-tech/tap/libdbc

# Or, with the archive downloaded from GitHub Releases
tar xzf libdbc-dev_*_darwin_arm64.tar.gz
export PKG_CONFIG_PATH="$PWD/$(echo libdbc-dev_*_darwin_arm64)"
```

Windows (x86_64, MSVC or GNU): download `libdbc-dev_<version>_windows_amd64.zip`
from GitHub Releases or from the Cloudsmith repository, unpack it, and point
`DBC_SYS_LIB_DIR` at the directory containing `libdbc.lib` (pkg-config is not
used on Windows):

```powershell
$v = "<version>"
Invoke-WebRequest -OutFile libdbc-dev.zip "https://dl.cloudsmith.io/public/columnar/dbc/raw/names/libdbc-dev-windows-x64/versions/$v/libdbc-dev_${v}_windows_amd64.zip"
Expand-Archive libdbc-dev.zip -DestinationPath .
$env:DBC_SYS_LIB_DIR = "$PWD\libdbc-dev_${v}_windows_amd64"
```

Set `PKG_CONFIG_PATH` to the directory containing `dbc.pc` for a nonstandard
installation prefix. The source code is at <https://github.com/columnar-tech/dbc>.

Then depend on the crate as usual:

```toml
[dependencies]
columnar-dbc = "0"
```

This mode discovers the `dbc` pkg-config module and does not download the
bundled platform crates. It is not limited to
the bundled targets below, but requires a libdbc built for your target.

The installed libdbc must match the crate's version: `columnar-dbc` 0.4.x
needs libdbc 0.4.x at or above the crate's patch release, and the build fails
otherwise, because libdbc's C interface can change between minor releases.

## Bundled prebuilt libraries

To link a prebuilt static library instead of an installed libdbc, enable the
`bundled` feature:

```toml
[dependencies]
columnar-dbc = { version = "0", features = ["bundled"] }
```

| Target | Requirement |
|---|---|
| `x86_64-unknown-linux-gnu`, `aarch64-unknown-linux-gnu` | glibc 2.28+ |
| `x86_64-apple-darwin`, `aarch64-apple-darwin` | macOS 12+ |
| `x86_64-pc-windows-msvc`, `x86_64-pc-windows-gnu` | Windows 10+ |

The `bundled` feature supports these targets; other targets fail at build time
with a list of supported targets. Each target downloads one
`columnar-dbc-sys-<platform>` crate (~4 MB) containing the prebuilt library,
and links about 11 MB into your binary.

## Library source selection

The library source is selected in this order:

1. `DBC_SYS_LIB_DIR` selects a custom library directory, without pkg-config.
2. The presence of `DBC_SYS_USE_PKG_CONFIG` selects pkg-config, even when the
   `bundled` feature is enabled.
3. The `bundled` feature selects the prebuilt platform library.
4. Otherwise, pkg-config is used (the default).

`DBC_SYS_USE_PKG_CONFIG` is a presence flag: even an empty value enables it.

System/custom linking is **static by default**. A custom directory must
contain `libdbc.a` (on Windows, `libdbc.a` or the zip's `libdbc.lib`); for example, `DBC_SYS_LIB_DIR=/opt/dbc/lib cargo build`.
The build can't check the version of a custom directory's library, so it must come
from the same dbc release as the crate.
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
`config.toml`, just like the CLI. If it is missing, dbc uses its built-in registries.
An unreadable or invalid global file is a construction error, not a warning.
The locations are:

| Platform | Global registry configuration |
|---|---|
| Linux | `$XDG_CONFIG_HOME/columnar/dbc/config.toml`, or `~/.config/columnar/dbc/config.toml` |
| macOS | `~/Library/Application Support/Columnar/dbc/config.toml` |
| Windows | `%APPDATA%\Columnar\dbc\config.toml` |

Use the `ClientConfig`'s builder methods to choose options:

```rust,no_run
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

- `base_url(url)` selects one registry when the URL is nonempty; an empty
  string is treated as omitted. `registries(vec![...])` selects that exact
  list, in order, including an empty list to disable all registries. Setting
  both a nonempty base URL and an explicit list is an error.
  Invalid registry URLs are errors.
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
  file-based configuration that leaves no registries is an error.

An empty registry list means no registries, like Go and TypeScript; search
returns no drivers. Omit `.registries(...)` to use the default configuration.
To load a project's configuration without the global file:

```rust,no_run
use columnar_dbc::{Client, ClientConfig, RegistryConfig};

let config = ClientConfig::default()
    .registry_config(RegistryConfig::None)
    .project_file("dbc.toml");
let client = Client::with_config(&config)?;
# Ok::<(), columnar_dbc::Error>(())
```

## Credentials

`CredentialStore::Default` uses credentials saved by `dbc auth login` or
the free `login(&Credential) -> Result<(), Error>` function. `login` stores a token
credential, replacing any entry for the same registry host; it does not
perform browser-based or interactive authentication. Optional
`refresh_token` and `client_id` values are saved too.

To inject a credential into one client instead of storing it:

```rust,no_run
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
used to authenticate. An injected credential's refreshed token is never
written to dbc's credential file; using it with Columnar's registry can still
write dbc's license file.

`logout(registry_url: &str) -> Result<(), Error>` removes the credential for the
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

`license_path() -> Result<PathBuf, Error>` returns the `columnar.lic` path beside
dbc's credential store, even if no license exists. It does not install a
license. `install_license(src_path: &Path, force: bool) -> Result<(), Error>`
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
