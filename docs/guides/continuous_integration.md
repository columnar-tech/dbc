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

# Continuous Integration

dbc works well in non-interactive environments such as on continuous integration (CI) platforms. You may also want to read through our [Version Control](./version_control.md) guide as these two concepts are related.

## GitHub Actions

We recommend using the [columnar-tech/setup-dbc](https://github.com/columnar-tech/setup-dbc) action if you're using [GitHub Actions](https://docs.github.com/en/actions) for CI.

As an example, here's a workflow that automatically installs all drivers listed in your [driver list](../concepts/driver_list.md) before running your tests:

```yaml
name: Test
on: [push]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6

      # Note: Automatically installs drivers specified in dbc.toml
      - uses: columnar-tech/setup-dbc@v1

      - name: Run tests
        run: pytest ...
```

See the [columnar-tech/setup-dbc README](https://github.com/columnar-tech/setup-dbc) for usage information and more examples.

## Other CI Systems

To use dbc with other CI systems, we recommend using our command line installers because they will always install the latest version of dbc for whatever platform you run them on.

As an example for you to adapt to your system, here's a GitHub Actions workflow that installs and makes dbc available without using [columnar-tech/setup-dbc](https://github.com/columnar-tech/setup-dbc):

{% raw %}
```yaml
name: Test
on: [push]

jobs:
  test:
    runs-on: ${{ matrix.os }}

    strategy:
      matrix:
        os: [ubuntu-latest, windows-latest, macos-latest]

    steps:
      - uses: actions/checkout@v6

      - name: Install dbc (Linux, macOS)
        if: runner.os != 'Windows'
        run: |
          curl -LsSf https://dbc.columnar.tech/install.sh | sh

      - name: Install dbc (Windows)
        if: runner.os == 'Windows'
        run: |
          powershell -ExecutionPolicy ByPass -c "irm https://dbc.columnar.tech/install.ps1 | iex"

      - name: Add dbc to PATH (Linux, macOS)
        if: runner.os != 'Windows'
        shell: bash
        run: echo "$HOME/.local/bin" >> $GITHUB_PATH

      - name: Add dbc to PATH (Windows)
        if: runner.os == 'Windows'
        shell: pwsh
        run: |
          Join-Path $env:USERPROFILE ".local\bin" | Out-File -FilePath $env:GITHUB_PATH -Encoding utf8 -Append

      - name: Run tests
        run: pytest ...
```
{% endraw %}
