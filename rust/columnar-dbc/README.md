# columnar-dbc

Rust bindings for [dbc](https://columnar.tech/dbc), the tool for finding,
installing, and verifying [ADBC](https://arrow.apache.org/adbc/) drivers.
dbc itself is statically linked: there is no `dbc` executable to install and
no Go toolchain needed at build time.

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

## Supported targets

| Target | Requirement |
|---|---|
| `x86_64-unknown-linux-gnu`, `aarch64-unknown-linux-gnu` | glibc 2.28+ |
| `x86_64-apple-darwin`, `aarch64-apple-darwin` | macOS 12+ |
| `x86_64-pc-windows-gnu` | MinGW-w64 (MSVC is not supported) |

Other targets fail at build time with a list of supported targets.
Each target downloads one `columnar-dbc-sys-<platform>` crate (~4 MB)
containing the prebuilt library, and links about 11 MB into your binary.

## Private registries

`Client::new()` uses credentials stored by `dbc auth login`. To inject a
token at runtime instead, use `Client::with_config` with a `Credential`.
