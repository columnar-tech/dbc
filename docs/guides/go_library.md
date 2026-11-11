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

# Go Library

The root Go package, `github.com/columnar-tech/dbc`, provides `dbc.NewClient`
for working with [driver registries](../concepts/driver_registry.md).
Without registry options, it uses the built-in public and private registries.
It does not discover or load global `config.toml` or project `dbc.toml` files
itself. Load those files explicitly when your application needs them.

## Loading registry configuration

Use `dbc.LoadGlobalConfig(configDir)` to read `config.toml` from a directory
your application supplies, then pass the result to `dbc.WithGlobalConfig`.
A missing global file returns `(nil, nil)`; other read errors, invalid TOML,
and malformed registry entries return errors.

Use `dbc.LoadProjectRegistries(path)` to read only the `registries` and
`replace_defaults` settings from a project's `dbc.toml`, without decoding its
driver specifications. Pass the returned entries and replacement flag to
`dbc.WithProjectRegistries`. A missing explicitly named project file is an
error, as are invalid TOML and malformed registry entries.

For example, an application that requires an explicitly named project file
can construct its client as follows:

```go
import "github.com/columnar-tech/dbc"

func newConfiguredClient(configDir, projectFile string) (*dbc.Client, error) {
    global, err := dbc.LoadGlobalConfig(configDir)
    if err != nil {
        return nil, err
    }
    entries, replaceDefaults, err := dbc.LoadProjectRegistries(projectFile)
    if err != nil {
        return nil, err
    }
    return dbc.NewClient(
        dbc.WithGlobalConfig(global),
        dbc.WithProjectRegistries(entries, replaceDefaults),
    )
}
```

When these configuration options apply, registries are merged in priority
order: project entries first, then global entries, then built-in defaults.
Duplicate registry URLs are removed, retaining the highest-priority entry.
`replace_defaults = true` omits the built-in defaults, not the global entries.
An omitted project replacement flag inherits the global setting; an explicit
`false` restores the built-in registries even if the global setting is `true`.

Both loaders validate registry URLs: each must be nonempty, have a host, and
use `http` or `https`. `dbc.NewClient` also validates entries supplied directly
through `dbc.WithGlobalConfig` and `dbc.WithProjectRegistries` when those
options apply. It returns an error if the merged configuration produces no
registries. The loaders do not reject `replace_defaults = true` with no entries
on its own, because the other configuration layer may supply entries.

## URL replacements

`dbc.WithURLReplacements([]dbc.URLReplacement{...})` sends requests for URLs
under a prefix to a mirror instead; see [Mirrors](../concepts/driver_registry.md#mirrors).
`dbc.WithGlobalConfig` applies the `network.url_replacements` rules of the
loaded `config.toml` the same way, and `dbc.LoadGlobalConfig` and `dbc.NewClient`
return an error for invalid rules. Replacements apply with `WithBaseURL` and
`WithRegistries` too. Requests that were replaced are anonymous. The deprecated
package-level functions use `dbc.SetDefaultURLReplacements`.

## Explicit registry overrides

`dbc.WithRegistries([]dbc.Registry{...})` replaces the entire registry list,
ignoring both global and project configuration options regardless of option
order. It preserves the supplied order and duplicates rather than merging or
deduplicating entries. An explicit empty slice (or `nil`) configures zero
registries; omitting `WithRegistries` leaves normal default/configuration
behavior in place.

```go
client, err := dbc.NewClient(dbc.WithRegistries([]dbc.Registry{}))
```

A nonempty `dbc.WithBaseURL("https://registry.example.com")` selects one
registry and takes precedence over `WithRegistries`, `WithGlobalConfig`, and
`WithProjectRegistries`. An empty base URL does not override the registry list.

Unlike configuration-file entries, the root Go client's explicit
`WithRegistries` entries are not URL-validated by `NewClient`. Callers must
supply valid, non-nil `BaseURL` values. `WithBaseURL` parses its string during
client construction and panics if URL parsing fails; it does not enforce the
configuration loaders' host and HTTP(S) checks. Do not assume invalid explicit
URLs produce a `NewClient` error.

Explicit options bypass validation and merging of the configuration objects
passed to `NewClient`; they cannot skip loader calls your application already
made. If you want explicit options to avoid file reads, do not call the loaders
on that path.

## Differences from language bindings

Filesystem-enabled bindings load the user's global configuration by default,
unlike the root Go client. In the Node binding, use `registryConfig: "none"`
to skip global configuration; an explicitly named `projectFile` is still
loaded. A nonempty `baseURL` or an explicit `registries` list skips both files
entirely, including validation of `registryConfig`. An empty `baseURL` string
is treated as omitted. An explicit `registries: []` means zero registries,
not the defaults.

The bindings reject supplying both a nonempty base URL and an explicit registry
list, and validate explicit registry URLs before constructing the client. These
checks differ from the root Go options described above. `registryConfig` and
`projectFile` are binding settings, not root Go client options.
