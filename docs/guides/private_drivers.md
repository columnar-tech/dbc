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

Most drivers available with dbc are hosted on Columnar's public [driver registry](../concepts/driver_registry.md). However, some of the drivers you see when you run `dbc search` may be marked with a `[private]` label.

To install and use these drivers, you must:

1. Log in to [Columnar Cloud](https://cloud.columnar.tech) with dbc
2. Start a trial license

Continue reading to learn how to log in and start a trial.

## Logging In

To log into Columnar's private driver registry, run `dbc auth login`. This will automatically create an account for you the first time you log in.

You will see the following in your terminal and your default web browser will be opened:

```console
$ dbc auth login
Opening https://auth.columnar.tech/activate?user_code=XXXX-XXXX in your default web browser...
⠏ Waiting for confirmation...
```

In your browser, you will see a **Device Confirmation** prompt and, once you click **Confirm**, you will be redirected to log in with the provider of your choice. Once you log in, you will be redirected to [Columnar Cloud](https://cloud.columnar.tech/). Keep the tab open and continue on to the next step.

## Starting a Trial

To install and use a private driver, you must start a trial and obtain a license. This is a separate step from logging in.

Licenses can be obtained from your [Account](https://cloud.columnar.tech/account) page on Columnar Cloud by clicking **Start Free 14-Day Trial**. Follow any instructions in the dialog that opens up and click **Accept** to create your license.

### Downloading Your License

dbc will automatically download your license if you:

1. Have an active license
2. Run `dbc install` with a private driver

If you'd prefer to download the license manually, you can click **Download License File** and place the downloaded file in the appropriate location for your operating system:

- Windows: `%LocalAppData%/dbc/credentials`
- macOS:  `~/Library/Application Support/Columnar/dbc/credentials`
- Linux: `~/.local/share/dbc/credentials`

You may also use a custom location by setting the environment variable `XDG_DATA_HOME` to an absolute path of your choosing. If you do this, you must ensure you set the same value of `XDG_DATA_HOME` when loading drivers with the [driver manager](../concepts/driver_manager.md) for the drivers to find your license.

## Logging Out

To log out, run `dbc auth logout`.

By default, the `logout` command doesn't purge any driver licenses from your system and only removes your login credentials. If you wish remove the local copy of your license run:

```console
$ dbc auth logout --purge
```

!!! note

    Note that this command only removes the local copy of your license and does not cancel any active licenses you may have in your [Columnar Cloud](https://cloud.columnar.tech) account.

!!! warning

    ADBC drivers that require a license (i.e., private drivers) will stop working after you run this command. You can re-download your license with `dbc auth login`. See [Downloading Your License](#downloading-your-license).


## API Keys

dbc also supports logging in to private driver registries via API key. This is primarily intended for use in [Continuous Integration](https://en.wikipedia.org/wiki/Continuous_integration) systems or any system where logging in with a web browser is not possible.

To create an API key, open a web browser to your [API keys](https://cloud.columnar.tech/apikeys) page.

!!! note inline end

    If you've already created an API key, you will see a **Create API Key** button instead.

If you haven't created any API keys before, you will see a **Create Your First API Key** button. After clicking it, enter a name, optionally choose an expiration, and click **Create**. On the following screen, you will see your new API key and instructions to copy it to your clipboard.

!!! note

    API keys grant full access to your account so be sure to store it in a secure way.


Then to use your API key to log in, run:

```console
$ dbc auth login --api-key "<YOUR_API_KEY_HERE>"
```

Once you've run this successfully, dbc is now logged in and you can install private drivers as you would normally.
