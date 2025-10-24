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

# dbc

dbc is a command-line tool for installing and managing [ADBC](https://arrow.apache.org/adbc) drivers.

<br/>Start by installing a driver:

=== "BigQuery"

    ```console
    $ dbc install bigquery
    ```

    <br/>Then [install a driver manager](./guides/driver_manager.md) and load it in any supported language:

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "bigquery", ... })
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

=== "DuckDB"

    ```console
    $ dbc install duckdb
    ```

    <br/>Then [install a driver manager](./guides/driver_manager.md) and load it in any supported language:

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "duckdb", ... })
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

    <br/>Then [install a driver manager](./guides/driver_manager.md) and load it in any supported language:

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "flightsql", ... })
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

    <br/>Then [install a driver manager](./guides/driver_manager.md) and load it in any supported language:

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "mssql", ... })
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

    <br/>Then [install a driver manager](./guides/driver_manager.md) and load it in any supported language:

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "mysql", ... })
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

    <br/>Then [install a driver manager](./guides/driver_manager.md) and load it in any supported language:

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "postgresql", ... })
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

    <br/>Then [install a driver manager](./guides/driver_manager.md) and load it in any supported language:

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "redshift", ... })
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

    <br/>Then [install a driver manager](./guides/driver_manager.md) and load it in any supported language:

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "snowflake", ... })
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

    <br/>Then [install a driver manager](./guides/driver_manager.md) and load it in any supported language:

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "sqlite", ... })
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

    <br/>Then [install a driver manager](./guides/driver_manager.md) and load it in any supported language:

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "trino", ... })
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

## Features

- Install pre-built [ADBC](https://arrow.apache.org/adbc) drivers with a single command
- Manage numerous drivers without conflicts
- Install drivers just for your user or system-wide
- Create reproducible environments with [driver list](concepts/driver_list.md) files
- Cross-platform: Runs on macOS, Linux, and Windows
- Installable with pip, Docker, and more (See [Installation](getting_started/installation.md))
- Works great in CI/CD environments

## Installation

Install dbc with our automated installer:

=== "macOS and Linux"

    ```console
    $ curl -LsSf https://dbc.columnar.tech/install.sh | sh
    ```

=== "Windows"

    ```console
    $ powershell -ExecutionPolicy ByPass -c "irm https://dbc.columnar.tech/install.ps1 | iex"
    ```

Then, take your [first steps](getting_started/first_steps.md) to get started using dbc.

!!! note

    See our [Installation](getting_started/installation.md) page for more ways to get dbc.
