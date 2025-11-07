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

# Python Notebooks

dbc can be installed and used directly in Python notebooks (such as Jupyter or Google Colab).
Each of the following code blocks is designed to be executed as an individual cell in your notebook.

Install the `dbc`, `adbc-driver-manager`, and `pyarrow` packages:

```python
%pip install dbc adbc_driver_manager pyarrow
```

Install the `duckdb` driver:

```python
!dbc install duckdb
```

!!! note

    This guide uses the DuckDB driver for simplicity.
    To list all available drivers, run `!dbc search`.

Import the `dbapi` module:

```python
from adbc_driver_manager import dbapi
```

Connect to a database via ADBC, create a cursor, execute queries, and fetch the result as a PyArrow Table:

```python
with (
    dbapi.connect(driver="duckdb", db_kwargs={"path": "penguins.duckdb"}) as con,
    con.cursor() as cursor,
):
    cursor.execute("CREATE TABLE IF NOT EXISTS penguins AS FROM 'http://blobs.duckdb.org/data/penguins.csv'")
    cursor.execute("SELECT * FROM penguins")
    table = cursor.fetch_arrow_table()
```

Print the table:

```python
print(table)
```
