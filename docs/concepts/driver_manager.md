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

# Driver Manager

The term "driver manager" refers to an [ADBC Driver Manager](https://arrow.apache.org/adbc/current/format/how_manager.html).

Driver managers load [ADBC drivers](driver.md) and provide a consistent API for using any driver. They're ideal for scenarios where an application needs to work with multiple drivers or use drivers written in a language that isn't the language the application is written in. However, using an ADBC driver with a driver manager is useful even if this isn't the case.

If you're familiar with standards like [ODBC](https://en.wikipedia.org/wiki/Open_Database_Connectivity) and [JDBC](https://en.wikipedia.org/wiki/Java_Database_Connectivity), you may have seen the term "driver manager" before in those ecosystems. ADBC driver managers are fundementally similar to driver managers in these systems (i.e., they load drivers) but there are some practical differences:

- In ODBC, driver managers are installed system-wide. In ADBC, a driver manager is just a library you use alongside your program.
- In JDBC, the driver manager is built into the language. In ADBC, driver managers are libraries that are available in many languages but must be installed separately.
