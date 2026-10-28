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

By default, dbc is configured to communicate with two driver registries: the public ADBC driver registry, which is affiliated with the [ADBC Driver Foundry](https://adbc-drivers.org) and hosted by Columnar, and Columnar's private driver registry. Most drivers will be from the public registry but some will be marked with a `[private]` label which means they're from the private registry. See [Private Drivers](../guides/private_drivers.md) for information on how to install and use private drivers.

- Public: [https://dbc-cdn.columnar.tech](https://dbc-cdn.columnar.tech)
- Private: [https://dbc-cdn-private.columnar.tech](https://dbc-cdn-private.columnar.tech)

!!! note

    If you access these registries without dbc, please set a user agent. See [Can I download drivers directly from the driver registries?](../getting_started/faq.md#can-i-download-drivers-directly-from-the-driver-registries).

When you run a command like [`dbc search`](../reference/cli.md#search) or [`dbc install`](../reference/cli.md#install), dbc gets information about the drivers that are available from each configured registry by downloading its `index.yaml` or using a cached copy.

## Go library configuration

Go callers can use `dbc.LoadProjectRegistries(path)` to read only the
`registries` and `replace_defaults` settings from a project's `dbc.toml`,
without decoding its driver specifications. Pass the returned entries and
replacement flag to `dbc.WithProjectRegistries`. An omitted replacement flag
inherits the global setting; an explicit `false` restores the built-in
registries. A missing explicitly named project file is an error.

The root Go `dbc.NewClient` does not discover configuration files itself:
load global settings with `dbc.LoadGlobalConfig(configDir)` and pass them with
`dbc.WithGlobalConfig` when needed. Project registries take priority over global
registries, followed by the built-in defaults unless replaced. Explicit
`dbc.WithBaseURL` or `dbc.WithRegistries` options bypass both configuration layers.
Explicit lists preserve order and duplicates; an empty list disables all registries.

Filesystem-enabled bindings load the user's global configuration by default,
unlike the root Go client. Use `registryConfig: "none"` to skip global
configuration; an explicitly named `projectFile` is still loaded. An explicit
base URL or registry list skips both files entirely, including validation of
`registryConfig`.
