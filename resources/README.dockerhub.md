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

dbc is a command-line tool for installing and managing [ADBC](https://arrow.apache.org/adbc) drivers.
This is the official set of Docker images for dbc.

## Usage

```sh
docker run -it --rm columnar/dbc:latest dbc --help
```

## Image tags

The following distroless images are available for Linux-based `amd64` and `arm64` architectures:

- `columnar/dbc:latest`
- `columnar/dbc:{major}.{minor}.{patch}`, e.g. `columnar/dbc:0.0.1`
