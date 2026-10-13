<!--
Copyright 2026 Columnar Technologies Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# Driver Registry

dbc installs drivers from a "driver registry" which is an internet-accessible index of installable [ADBC driver](./driver.md) packages.

By default, dbc is configured to communicate with Columnar's public and private driver registries. Most drivers will be from the public registry but some will be marked with a `[private]` label which means they're from the private registry. See [Private Drivers](../guides/private_drivers.md) for information on how to install and use private drivers.

When you run a command like [`dbc search`](../reference/cli.md#search) or [`dbc install`](../reference/cli.md#install), dbc gets information about the drivers that are available from each configured registry by downloading its `index.yaml` or using a cached copy.

## Configuring registries

{{ since_version('v0.4.0') }}

Alongside the built-in default registries, you can configure dbc to use additional driver registries at two levels:

- **Global**: a `config.toml` in your user config directory applies to every dbc command.
- **Project**: a `[[registries]]` section in a project's [`dbc.toml`](../reference/driver_list.md#registries) applies to dbc commands run in that project.

The global `config.toml` lives in your user config directory:

- Linux: `~/.config/columnar/dbc/config.toml`
- macOS: `~/Library/Application Support/Columnar/dbc/config.toml`
- Windows: `%AppData%\Columnar\dbc\config.toml`

Both files use the same `[[registries]]` syntax. Each entry requires a `url` (which must be an `http` or `https` URL) and may include an optional `name` that is shown as a `[name]` tag in `dbc search` output:

```toml
[[registries]]
url = "https://my-registry.example.com"
name = "my-registry"
```

### Priority and conflicts

When the same driver is published by more than one registry, dbc resolves the conflict by registry priority, highest first:

1. Project registries (`dbc.toml`)
2. Global registries (`config.toml`)
3. Built-in default registries

Registries are deduplicated by URL and, when a driver appears in more than one registry, the highest-priority registry wins.

### Replacing the default registries

Set `replace_defaults = true` to drop the built-in default registries and use only the registries you configure:

```toml
replace_defaults = true

[[registries]]
url = "https://my-registry.example.com"
name = "my-registry"
```

In a global `config.toml`, `replace_defaults` is either `true` or `false` (the default). In a project `dbc.toml` it has a third state — omitting it inherits the global config's value, `true` drops the built-in defaults, and `false` forces the built-in defaults back on even when the global config set `replace_defaults = true`.

### Which commands use which registries

- Global `config.toml` registries are used by **every** dbc command.
- Project `dbc.toml` registries are used by the commands that manage or discover drivers for the current project — [`dbc add`](../reference/cli.md#add), [`dbc sync`](../reference/cli.md#sync), [`dbc search`](../reference/cli.md#search), [`dbc info`](../reference/cli.md#info), and [`dbc docs`](../reference/cli.md#docs) — which read `dbc.toml` from the current directory.
- [`dbc install`](../reference/cli.md#install) installs into a user- or system-level [config level](../reference/config_level.md) rather than the project, so it uses only the global and built-in default registries and does not read a project `dbc.toml`.

Setting the `DBC_BASE_URL` environment variable overrides all registry configuration and points dbc at a single registry.
