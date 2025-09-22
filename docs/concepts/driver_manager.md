<!-- Copyright (c) 2025 Columnar Technologies Inc.  All rights reserved. -->

# Driver Manager

The term "driver manager" refers to an [ADBC Driver Manager](https://arrow.apache.org/adbc/current/format/how_manager.html).

Driver managers load [ADBC drivers](driver.md) and provide a consistent API for using any driver. They're ideal for scenarios where an application needs to work with multiple drivers or use drivers written in a language that isn't the language the application is written in. However, using an ADBC driver with a driver manager is useful even if this isn't the case.

If you're familiar with standards like [ODBC](https://en.wikipedia.org/wiki/Open_Database_Connectivity) and [JDBC](https://en.wikipedia.org/wiki/Java_Database_Connectivity), you may have seen the term "driver manager" before in those ecosystems. ADBC driver managers are fundementally similar to driver managers in these systems (i.e., they load drivers) but there are some practical differences:

- In ODBC, driver managers are installed system-wide. In ADBC, a driver manager is just a library you use alongside your program.
- In JDBC, the driver manager is built into the language. In ADBC, driver managers are libraries that are available in many languages but must be installed separately.
