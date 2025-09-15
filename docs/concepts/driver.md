<!-- Copyright (c) 2025 Columnar Technologies.  All rights reserved. -->

# Driver

In the context of dbc, "driver" means "ADBC driver." It's ADBC drivers that dbc lets you install and manage. [ADBC](https://arrow.apache.org/adbc) is part of the [Apache Arrow](https://arrow.apache.org) project and is a free and open standard. dbc builds on top of it.

!!! note

    What an ADBC driver actual is is entirely defined by the [ADBC project](https://arrow.apache.org/adbc), so we'll give a simple definition and then refer you to the ADBC project itself if you're interested in going deeper.

## What Is an ADBC Driver?

At a high level, an ADBC driver is a library that wraps the client for the database you want to use and exposes that database to you with a consistent API: the [ADBC API](https://arrow.apache.org/adbc/main/format/specification.html).

For example, if you're using the [ADBC SQLite Driver](https://arrow.apache.org/adbc/main/driver/sqlite.html) and you want to run a SQL query, you'd call two functions (in order):

- [`AdbcStatementSetSqlQuery`](https://arrow.apache.org/adbc/main/cpp/api/group__adbc-statement-sql.html#ga40254bb2c39711f5d2772cb78f349e4a)
- [`AdbcStatementExecuteQuery`](https://arrow.apache.org/adbc/main/cpp/api/group__adbc-statement.html#ga1f653045678d9d5d51780e37e3b644a6)

Inside the driver, these two functions call corresponding functions in the [SQLite API](https://www.sqlite.org/cintro.html):

- [`sqlite3_prepare`](https://www.sqlite.org/c3ref/prepare.html)
- [`sqlite_step`](https://www.sqlite.org/c3ref/step.html)

While there's no hard requirement for a driver to have a 1:1 correspondence like above, hopefully the it helps explain that there's no magic.

## More Resources

If you're interested in learning more about ADBC drivers or ADBC, check out these two pages:

- [How Drivers and the Driver Manager Work Together](https://arrow.apache.org/adbc/main/format/how_manager.html)
- [ADBC Frequently Asked Questions](https://arrow.apache.org/adbc/main/faq.html)
