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

dbc is the command-line tool for installing and managing [ADBC](https://arrow.apache.org/adbc) drivers. Get up and running with ADBC in just three steps:

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
    $ winget install Columnar.dbc
    ```

    !!! note

        If you installed dbc 0.1.0 with WinGet, uninstall the system-level package first and then reinstall 0.2.0:

        ```console
        $ winget uninstall --id Columnar.dbc
        $ winget install Columnar.dbc
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
    $ brew install columnar-tech/tap/dbc
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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "bigquery", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "bigquery")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "bigquery")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("bigquery", ... )
        ```

=== "ClickHouse"

    ```console
    $ dbc install clickhouse
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "clickhouse", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "clickhouse", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "clickhouse");
        ```

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "clickhouse", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "clickhouse")
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="clickhouse", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("clickhouse")
        ```

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "clickhouse")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("clickhouse", ... )
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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "databricks", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "databricks")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "databricks")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("databricks", ... )
        ```

=== "DataFusion"

    ```console
    $ dbc install datafusion
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "datafusion", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "datafusion", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "datafusion");
        ```

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "datafusion", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "datafusion")
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="datafusion", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("datafusion")
        ```

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "datafusion")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("datafusion", ... )
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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "duckdb", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "duckdb")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "duckdb")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("duckdb", ... )
        ```

=== "Exasol"

    ```console
    $ dbc install exasol
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "exasol", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "exasol", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "exasol");
        ```

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "exasol", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "exasol")
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="exasol", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("exasol")
        ```

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "exasol")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("exasol", ... )
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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "flightsql", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "flightsql")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "flightsql")
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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "mssql", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "mssql")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "mssql")
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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "mysql", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "mysql")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "mysql")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("mysql", ... )
        ```

=== "Oracle"

    ```console
    $ dbc install oracle
    ```

    !!! note

        Oracle is currently available as a private driver. Before installing and using it, run `dbc auth login` and start a trial license in Columnar Console. See [Private Drivers](./guides/private_drivers.md) for details.

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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "oracle", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "oracle")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "oracle")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("oracle", ... )
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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "postgresql", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "postgresql")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "postgresql")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("postgresql", ... )
        ```

=== "Quack"

    ```console
    $ dbc install --pre quack
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "quack", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "quack", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "quack");
        ```

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "quack", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "quack")
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="quack", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("quack")
        ```

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "quack")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("quack", ... )
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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "redshift", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "redshift")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "redshift")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("redshift", ... )
        ```

=== "SingleStore"

    ```console
    $ dbc install --pre singlestore
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "singlestore", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "singlestore", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "singlestore");
        ```

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "singlestore", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "singlestore")
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="singlestore", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("singlestore")
        ```

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "singlestore")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("singlestore", ... )
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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "snowflake", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "snowflake")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "snowflake")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("snowflake", ... )
        ```

=== "Spark"

    ```console
    $ dbc install spark
    ```

    <br/>3. [Install a driver manager](./guides/driver_manager.md) and load drivers in any supported language:

    === "C++"

        ```cpp
        #include <arrow-adbc/adbc.h>

        AdbcDatabaseSetOption(&database, "driver", "spark", &error)
        ```

    === "Go"

        ```go
        import . "github.com/apache/arrow-adbc/go/adbc/drivermgr"

        db, _ := Driver{}.NewDatabase(map[string]string{"driver": "spark", ... })
        ```

    === "Java"

        ```java
        import org.apache.arrow.adbc.driver.jni.JniDriver;

        JniDriver.PARAM_DRIVER.set(params, "spark");
        ```

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "spark", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "spark")
        ```

    ===+ "Python"

        ```python
        from adbc_driver_manager import dbapi

        con = dbapi.connect(driver="spark", ... )
        ```

    === "R"

        ```r
        library(adbcdrivermanager)

        drv <- adbc_driver("spark")
        ```

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "spark")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("spark", ... )
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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "sqlite", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "sqlite")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "sqlite")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("sqlite", ... )
        ```

=== "Teradata"

    ```console
    $ dbc install teradata
    ```

    !!! note

        Teradata is currently available as a private driver. Before installing and using it, run `dbc auth login` and start a trial license in Columnar Console. See [Private Drivers](./guides/private_drivers.md) for details.

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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "teradata", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "teradata")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "teradata")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("teradata", ... )
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

    === "JS/TS"

        ```javascript
        import { AdbcDatabase } from "@apache-arrow/adbc-driver-manager";

        const db = new AdbcDatabase({driver: "trino", ... });
        ```

        !!! note

            The JavaScript/TypeScript ADBC driver manager is for server-side runtimes like Node.js, Deno, and Bun. It does not run in the browser.

    === "Kotlin"

        ```kotlin
        import org.apache.arrow.adbc.driver.jni.JniDriver

        JniDriver.PARAM_DRIVER.set(params, "trino")
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

    === "Ruby"

        ```ruby
        require "adbc"

        database.set_option("driver", "trino")
        ```

    === "Rust"

        ```rust
        use adbc_driver_manager::ManagedDriver;

        let mut driver = ManagedDriver::load_from_name("trino", ... )
        ```

<br/>For a more detailed walkthrough on how to use dbc, check out our [First steps](./getting_started/first_steps.md) page or any of our [Guides](./guides/index.md).

## Features

- Install pre-built [ADBC](https://arrow.apache.org/adbc) drivers with a single command
- Manage numerous drivers without conflicts
- Install drivers just for your user or system-wide
- Create reproducible environments with [driver list](concepts/driver_list.md) files
- Cross-platform: Runs on macOS, Linux, and Windows
- Installable with pip, Docker, and more (See [Installation](./getting_started/installation.md))
- Works great in CI/CD environments (See [Continuous Integration](./guides/continuous_integration.md))

## Help

- Join the [Columnar Community Slack](https://join.slack.com/t/columnar-community/shared_invite/zt-3gt5cb69i-KRjJj~mjUZv5doVmpcVa4w)
- Open an [issue](https://github.com/columnar-tech/dbc/issues) or start a [discussion](https://github.com/columnar-tech/dbc/discussions) on GitHub
- Check out the [ADBC Quickstarts](https://github.com/columnar-tech/adbc-quickstarts)
