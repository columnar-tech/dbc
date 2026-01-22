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

# Private Drivers

{{ since_version('v0.2.0') }}

Most drivers available with dbc are hosted on Columnar's public [driver registry](../reference/driver_registry.md). However, some of the drivers you see when you run `dbc search` may be marked with a `[private]` label.

To use these drivers, you must:

1. Log in to [Columnar Cloud](https://cloud.columnar.tech) with dbc
2. Start a trial license

Continue reading to learn how to log in start a trial.

## Logging In

To log into Columnar's private driver registry, run `dbc auth login`. You will see the following in your terminal and your default web browser will be opened:

```console
$ dbc auth login
Opening https://auth.columnar.tech/activate?user_code=XXXX-XXXX in your default web browser...
⠏ Waiting for confirmation...
```

In your browser, you will see a **Device Confirmation** prompt and, once you click **Confirm**, you will be redirected to log in with the provider of your choice. Once you log in, you will be redirected to [Columnar Cloud](https://cloud.columnar.tech/). Keep the tab open and continue on to the next step.

## Starting a Trial

While you can install private drivers without a trial, you must have a license to use one. This is a separate step from logging in.

Licenses can be obtained from your [Account](https://cloud.columnar.tech/account) page on Columnar Cloud by clicking **Start Free 14-Day Trial**. Follow any instructions in the dialog that opens up and click **Accept** to create your license.

!!! warning

    dbc can automatically obtain your license but only if you run `dbc auth logout` and run `dbc auth login` again after starting your trial.

    For example, you will most likely follow these steps the first time you

    1. Run `dbc auth login`
    2. Start a trial license on your [Account](https://cloud.columnar.tech/account) page
    3. Run `dbc auth logout`
    4. Run `dbc auth login`

If you'd prefer to download the license manually, you can click **Download License File** and place the downloaded file in the appropriate location for your operating system:

- Windows: `%LocalAppData%/dbc/credentials`
- macOS:  `~/Library/Application Support/Columnar/dbc/credentials`
- Linux: `~/.local/share/dbc/credentials`

You may also use a custom location by setting the environment variable `XDG_DATA_HOME` to an absolute path of your choosing.

## Logging Out

To log out, run `dbc auth logout`.

By default, the `logout` command doesn't purge any driver licenses from your system and only removes your login credentials. If you wish remove the local copy of your license run:

```console
$ dbc auth logout --purge
```

Note that this command only removes the local copy of your license and does not cancel your trial.
