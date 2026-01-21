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

Most drivers available with dbc are hosted on Columnar's public [driver registry](../reference/driver_registry.md). Some of the drivers you see when you run `dbc search` may be marked with a `[private]` label. These drivers require logging in to install and a license to use.

## Logging In

To log into Columnar's private driver registry, run `dbc auth login`. You will see the following in your terminal and your default web browser will be opened:

```console
$ dbc auth login
Opening https://auth.columnar.tech/activate?user_code=XXXX-XXXX in your default web browser...
⠏ Waiting for confirmation...
```

In your browser, you will see a **Device Confirmation** prompt and, once you click **Continue**, you will be redirected to log in with your login provider of choice.

## Starting a Trial

To use any drivers you install marked with `[private]`, you must obtain a obtain a license. Licenses can be obtained from your [Account](https://cloud.columnar.tech/account) page by clicking **Start Free 14-Day Trial**.

Once your license is created in the web interface, you do not need to download the license manually. If you ran `dbc auth login` before starting a license, you will need to run `dbc auth logout` and then run `dbc auth login` again to download your license.
