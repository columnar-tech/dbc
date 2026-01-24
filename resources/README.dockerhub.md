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

[dbc](https://columnar.tech/dbc) is a command-line tool for installing and managing [ADBC](https://arrow.apache.org/adbc) drivers.
This is the official set of Docker images for dbc.

Note: These images are intended to be an easy way to run dbc and aren't designed for running typical analytical workloads inside the container. We recommend building your own images for more complicated use cases.

## Usage

To run dbc and have it print its usage:

```sh
docker run -it --rm columnar/dbc:latest --help
```

To search for drivers,

```sh
docker run -it --rm columnar/dbc:latest search
```

To install a driver, a few extra flags must be set. The reason for this is that dbc's docker images are based on the scratch image which has no filesystem.

Instead of attempting to install a driver into the container (which will fail), we mount a folder from our host (`$(pwd)/drivers`) into the container and specify that dbc should use that by setting the `ADBC_DRIVER_PATH` environment variable:

```sh
docker run --rm \
    -v $(pwd)/drivers:/drivers \
    -e ADBC_DRIVER_PATH=/drivers \
    dbc:latest install sqlite
```

You should now see the sqlite driver installed _outside_ of the container,

```sh
$ tree drivers
drivers
├── sqlite_linux_arm64_v1.10.0
│   ├── libadbc_driver_sqlite.so
│   ├── libadbc_driver_sqlite.so.sig
│   ├── LICENSE.txt
│   └── NOTICE.txt
└── sqlite.toml
```

## Image tags

The following distroless images are available for Linux-based `amd64` and `arm64` architectures:

- `columnar/dbc:latest`
- `columnar/dbc:{major}`, e.g. `columnar/dbc:1`
- `columnar/dbc:{major}.{minor}`, e.g. `columnar/dbc:0.1`
- `columnar/dbc:{major}.{minor}.{patch}`, e.g. `columnar/dbc:0.0.1`
