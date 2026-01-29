---
title: Documentation
---

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

# dbc

dbc is a command-line tool for installing and managing [ADBC](https://arrow.apache.org/adbc) drivers. Get up and running with ADBC in just three steps:

<br/>1. Install dbc (see [Installation](./getting_started/installation.md) for more options):

=== "Linux/macOS shell"

    ```console
    $ curl -LsSf https://dbc.columnar.tech/install.sh | sh
    ```

=== "Windows shell"

    ```console
    $ powershell -ExecutionPolicy ByPass -c "irm https://dbc.columnar.tech/install.ps1 | iex"
    ```

=== "Windows MSI"

    Download <https://dbc.columnar.tech/latest/dbc-latest-x64.msi> and then run the installer.

=== "WinGet"

    ```console
    $ winget install dbc
    ```

=== "uv"

    ```console
    $ uv tool install dbc
    ```

=== "pipx"

    ```console
    $ pipx install dbc
    ```

=== "Homebrew"

    ```console
    $ brew tap columnar-tech/tap
    $ brew install --cask dbc
    ```

<br/>2. Use dbc to install drivers:

=== "BigQuery"

    ```console
    $ dbc install bigquery
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "bigquery", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "bigquery", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "bigquery");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="bigquery", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("bigquery")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("bigquery", ... )
        ```

=== "Databricks"

    ```console
    $ dbc install databricks
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "databricks", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "databricks", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "databricks");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="databricks", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("databricks")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("databricks", ... )
        ```

=== "DuckDB"

    ```console
    $ dbc install duckdb
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "duckdb", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "duckdb", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "duckdb");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="duckdb", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("duckdb")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("duckdb", ... )
        ```

=== "Flight SQL"

    ```console
    $ dbc install flightsql
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "flightsql", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "flightsql", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "flightsql");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="flightsql", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("flightsql")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("flightsql", ... )
        ```

=== "SQL Server"

    ```console
    $ dbc install mssql
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "mssql", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "mssql", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "mssql");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="mssql", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("mssql")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("mssql", ... )
        ```

=== "MySQL"

    ```console
    $ dbc install mysql
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "mysql", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "mysql", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "mysql");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="mysql", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("mysql")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("mysql", ... )
        ```

=== "PostgreSQL"

    ```console
    $ dbc install postgresql
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "postgresql", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "postgresql", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "postgresql");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="postgresql", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("postgresql")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("postgresql", ... )
        ```

=== "Redshift"

    ```console
    $ dbc install redshift
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "redshift", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "redshift", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "redshift");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="redshift", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("redshift")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("redshift", ... )
        ```

=== "Snowflake"

    ```console
    $ dbc install snowflake
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "snowflake", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "snowflake", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "snowflake");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="snowflake", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("snowflake")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("snowflake", ... )
        ```

=== "SQLite"

    ```console
    $ dbc install sqlite
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "sqlite", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "sqlite", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "sqlite");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="sqlite", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("sqlite")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("sqlite", ... )
        ```

=== "Trino"

    ```console
    $ dbc install trino
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "trino", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "trino", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "trino");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="trino", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("trino")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("trino", ... )
        ```
=== "Oracle Database"

    ```console
    $ dbc install oracle
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "oracle", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "oracle", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "oracle");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="oracle", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("oracle")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("oracle", ... )


=== "Teradata"

    ```console
    $ dbc install teradata
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "teradata", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "teradata", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "teradata");
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="teradata", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("teradata")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("teradata", ... )

<br/>For a more detailed walkthrough on how to use dbc, check out our [First steps](./getting_started/first_steps.md) page or any of our [Guides](./guides/index.md).

## Features

- Install pre-built [ADBC](https://arrow.apache.org/adbc) drivers with a single command
- Manage numerous drivers without conflicts
- Install drivers just for your user or system-wide
- Create reproducible environments with [driver list](concepts/driver_list.md) files
- Cross-platform: Runs on macOS, Linux, and Windows
- Installable with pip, Docker, and more (See [Installation](./getting_started/installation.md))
- Works great in CI/CD environments

## Help

- Join the [Columnar Community Slack](https://join.slack.com/t/columnar-community/shared_invite/zt-3gt5cb69i-KRjJj~mjUZv5doVmpcVa4w)
- Open an [issue](https://github.com/columnar-tech/dbc/issues) or start a [discussion](https://github.com/columnar-tech/dbc/discussions) on GitHub
- Check out the [ADBC Quickstarts](https://github.com/columnar-tech/adbc-quickstarts)
