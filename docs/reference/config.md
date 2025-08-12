<!-- Copyright (c) 2025 Columnar Technologies.  All rights reserved. -->

# Config

TODO

## Environment

`--level` value `env`.

If the `ADBC_CONFIG_PATH` environment variable is set, tries to install to that path.

## User

`--level` value `user`.

- On Linux (and other Unix-like platforms), this is `$XDG_CONFIG_HOME/adbc` (if `$XDG_CONFIG_HOME` is set) or `~/.config/adbc`.
- On macOS, this is `~/Library/Application Support/ADBC`.
- On Windows, this is either the registry under `HKEY_CURRENT_USER\SOFTWARE\ADBC\Drivers\` or `%LOCAL_APPDATA%\ADBC\drivers`.

## System

`--level` value `system`.

!!! note

    Depending on your environment, you may need elevated privileges to use the `--level system` option (such as `sudo` on Unix-likes and Administrator on Windows).

- On Linux (and other Unix-like platforms), this is `/etc/adbc`.
- On macOS, this is `/Library/Application Support/ADBC`.
- On Windows, this is in the registry under `HKEY_LOCAL_MACHINE\SOFTWARE\ADBC\Drivers\`

## More Info

See [ADBC Driver Manager and Manifests](https://arrow.apache.org/adbc/main/format/driver_manifests.html) for more detail.
