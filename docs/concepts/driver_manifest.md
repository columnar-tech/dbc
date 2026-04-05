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

# Driver Manifest

The term "driver manifest" refers to an [ADBC Driver Manfifest](https://arrow.apache.org/adbc/current/format/driver_manifests.html).

In short, a driver manifest is a metadata file that stores key information about a driver, including the information a [Driver Manager](./driver_manager.md) needs to load it.

For example, here's an example driver manifest for the MySQL ADBC driver:

```toml
manifest_version = 1
name = 'ADBC Driver Foundry Driver for MySQL'
publisher = 'ADBC Drivers Contributors'
license = 'Apache-2.0'
version = '0.1.0'
source = 'dbc'

[ADBC]
version = '1.1.0'

[Driver]
[Driver.shared]
macos_arm64 = '/Users/user/Library/Application Support/ADBC/Drivers/mysql_macos_arm64_v0.1.0'
```

Many details about how driver manifests work are outlined in the [ADBC Driver Manifests](https://arrow.apache.org/adbc/current/format/driver_manifests.html) documentation.
