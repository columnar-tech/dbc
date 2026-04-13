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

# Version Control

When using dbc in projects where version control software such as [git](https://git-scm.com) is being used, we recommend the following:

- Use a [driver list](../concepts/driver_list.md) to record drivers and their version constraints instead of installing drivers manually with [`dbc install`](./installing.md)
- Track `dbc.toml` with version control and always use `dbc sync` to install drivers after checkout
- To maximize reproducibility, also track [`dbc.lock`](./driver_list.md#lockfile)
- Don't track installed driver directories with version control, use `dbc.toml` instead

## Example Workflow

To help illustrate how this works in practice, see the example below for how to use dbc when collaborating with git. This assumes both developers have already [installed](../getting_started/installation.md) dbc.

Developer 1 sets up dbc with the drivers their project needs:

```console
# Create a driver list file
$ dbc init

# Add the mysql and sqlite drivers to it. Constrain sqlite's version.
$ dbc add mysql "sqlite<2"
added mysql to driver list
added sqlite to driver list with constraint <2
use `dbc sync` to install the drivers in the list

# Install the drivers from dbc.toml
$ dbc sync
✓ mysql-0.1.0
✓ sqlite-1.11.0
Done!

# Start tracking dbc.toml with git
$ git add dbc.toml

# Commit and push
$ git commit -m "Create dbc.toml"
$ git push
```

Developer 2 then clones the repository and uses `dbc sync`:

```console
$ git clone example/repo

# Install the drivers from dbc.toml
$ dbc sync
✓ mysql-0.1.0
✓ sqlite-1.11.0
Done!
```

Now, at this point, both Developer 1 and Developer 2 have the same set of drivers available on their systems.
