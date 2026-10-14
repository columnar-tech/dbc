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

# Environment Variables

dbc supports the following environment variables:

## `DBC_BASE_URL`

Base URL of the driver registry to use. When set, overrides the default registries.

## `DBC_OAUTH_CLIENT_ID`

OAuth client ID for [`dbc auth login`](cli.md#login).

## ADBC Variables

dbc respects the following environment variables that [ADBC](https://arrow.apache.org/adbc) driver managers respect:

### `ADBC_DRIVER_PATH`

When set, drivers are installed to the specified path. See [Config Level](config_level.md#default-behavior).

### `VIRTUAL_ENV`

When set, drivers are installed inside the Python virtual environment specified by the value unless `ADBC_DRIVER_PATH` is set. See [Config Level](config_level.md#default-behavior).

### `CONDA_PREFIX`

When set, drivers are installed inside the Conda environment specified by the value unless `ADBC_DRIVER_PATH` or `VIRTUAL_ENV` are set. See [Config Level](config_level.md#default-behavior).

## Other Variables

dbc supports the following general environment variables:

### `HTTP_PROXY`

Proxy URL for `http://` requests.

### `HTTPS_PROXY`

Proxy URL for `https://` requests.

### `NO_PROXY`

Hosts that bypass the proxy.

### `NO_COLOR`

When set to a truthy value (e.g., `true`, `1`), disables colored output.
