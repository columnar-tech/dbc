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

# Supported Platforms

[dbc](../index.md) is supported on the following platforms:

- macOS (Apple Silicon)
- macOS (Intel)
- Linux (x86_64)
- Linux (aarch64)
- Windows (x86_64)

dbc is developed, tested, and packaged for these platforms. If you find any problems or would like to request your platform be included, please file an [Issue](https://github.com/columnar-tech/dbc/issues).

## Driver Support

[Drivers](../concepts/driver.md) that you can [install](../guides/installing.md) with dbc are generally available for all of the above platforms.
When dbc installs a driver, it automatically detects your platform and tries to find a [driver package](../concepts/driver_manifest.md) matching your system. If a driver isn't available for your platform, you'll receive an error.

For example, on arm64 Windows you would get this error:

```console
$ dbc install sqlite
Error: no package found for platform 'windows_arm64'
```
