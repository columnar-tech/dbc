<!--
Copyright 2025 Columnar Technologies Inc.

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

dbc installs drivers from a "driver registry" which is an internet-accessible index of installable [ADBC driver](./driver.md) packages. Currently, dbc supports a single driver registry which is located at [https://dbc-cdn.columnar.tech](https://dbc-cdn.columnar.tech) and is managed by [Columnar](https://columnar.tech).

When you run a command like [`dbc search`](../reference/cli.md#search) or [`dbc install`](../reference/cli.md#install), dbc downloads an `index.yaml` from [https://dbc-cdn.columnar.tech](https://dbc-cdn.columnar.tech/index.yaml) (or uses a cached copy) in order to find the appropriate driver for your command.
