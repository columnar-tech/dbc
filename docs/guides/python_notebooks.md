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

# Python Notebooks

[dbc](../index.md) can be [installed](../getting_started/installation.md) and used directly in Python notebooks (such as [Jupyter](https://jupyter.org) or [Google Colab](https://colab.google)).
Each of the following code blocks is designed to be executed as an individual cell in your notebook.

Install the `dbc`, `adbc-driver-manager`, and `pyarrow` packages:

```python
%pip install dbc adbc_driver_manager pyarrow
```

Install the `duckdb` [driver](../concepts/driver.md):

```python
!dbc install duckdb
```

!!! note

    This guide uses the [DuckDB driver](https://arrow.apache.org/adbc/current/driver/duckdb.html) for simplicity.
    To list all available drivers, run `!dbc search`. See the [Finding Drivers](./finding_drivers.md) guide for more information.

Import the `dbapi` module from the [ADBC driver manager](../concepts/driver_manager.md):

```python
from adbc_driver_manager import dbapi
```

Connect to a database via [ADBC](https://arrow.apache.org/adbc/), create a cursor, execute queries, and fetch the result as a [PyArrow](https://arrow.apache.org/docs/python/) Table:

```python
with (
    dbapi.connect(driver="duckdb") as con,
    con.cursor() as cursor,
):
    cursor.execute("CREATE TABLE IF NOT EXISTS penguins AS FROM 'https://blobs.duckdb.org/data/penguins.csv'")
    cursor.execute("SELECT * FROM penguins")
    table = cursor.fetch_arrow_table()
```

Print the table:

```python
print(table)
```
