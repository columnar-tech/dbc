<!-- Copyright (c) 2025 Columnar Technologies.  All rights reserved. -->

# Driver Manager

The term "driver manager" refers to an [ADBC Driver Manager](https://arrow.apache.org/adbc/current/format/how_manager.html).

Driver managers load [ADBC drivers](driver.md) and provide a consistent API for using any driver. They're ideal for scenarios where an application needs to work with multiple drivers or drivers written in a language that isn't the language the application is written in. However, using an ADBC driver with a driver manager is useful even if this isn't the case.
