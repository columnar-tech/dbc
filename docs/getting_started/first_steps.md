<!-- Copyright (c) 2025 Columnar Technologies Inc.  All rights reserved. -->

# First Steps

Once you've [installed dbc](./installation.md), the next step is to install an [ADBC](https://arrow.apache.org/adbc) driver and run some queries with it.

On this page, we'll break down using dbc into three steps:

1. Installing an ADBC driver
2. Loading the driver with an ADBC driver manager
3. Using the driver to run queries

The process will be similar no matter which ADBC driver you are using but, for the purposes of this guide, we'll be using the BigQuery ADBC driver.

Once you're finished, you will have successfully installed, loaded, and used the BigQuery ADBC driver to query a [BigQuery public dataset](https://cloud.google.com/bigquery/public-data).

## Pre-requisites

To run through the steps on this page, you'll need at a minimum,

- dbc (See [Installation](./installation.md))
- A recent Python installation with pip
- The [Google Cloud cli](https://cloud.google.com/cli) and a Google account to use it with

## Setup

### Create a Google Cloud Project

You'll need to create a Project in the Google Cloud Console before continuing. See [Create a Google Cloud Project](https://developers.google.com/workspace/guides/create-project) for details on how to do this.
For convenience, the steps are included below:

1. Log into your [Google Cloud Console](https://console.cloud.google.com/)
2. Create a new Project. There are a few ways to do this,

    - With Menu > IAM & Admin > Create a Project
    - With the "Select a project" picker, click "New Project"
    - With the "Create or select a project" button. This option may not be visible.

3. Give the new project a name and ID (or use the defaults) and save the ID somewhere for later. Note the name is not the same as the ID.

You can also refer to the [BigQuery public dataset](https://cloud.google.com/bigquery/public-data) page for more details.

### Authenticate with the gcloud CLI

To access BigQuery, you'll need to save credentials locally for the BigQuery driver to use.
With the [Google Cloud cli](https://cloud.google.com/cli) installed, log in by running:

```console
$ gcloud auth application-default login
```

Your browser should open, prompting you to log into your Google Account and grant access. Click Continue and authentication should continue and complete.

If all went well, your credentials are now saved locally and the BigQuery driver will automatically find them.

## Installing a Driver

Let's use dbc to install the BigQuery ADBC driver.

First, run `dbc search` to find the exact name of the driver:

```console
$ dbc search
• bigquery - An ADBC driver for Google BigQuery developed by the ADBC Driver Foundry
• duckdb - An ADBC driver for DuckDB developed by the DuckDB Foundation
• flightsql - An ADBC driver for Apache Arrow Flight SQL developed under the Apache Software Foundation
• mssql - An ADBC driver for Microsoft SQL Server developed by Columnar
• mysql - An ADBC Driver for MySQL developed by the ADBC Driver Foundry
• postgresql - An ADBC driver for PostgreSQL developed under the Apache Software Foundation
• redshift - An ADBC driver for Amazon Redshift developed by Columnar
• sqlite - An ADBC driver for SQLite developed under the Apache Software Foundation
```

From the output, you can see that the name you'll need is `"bigquery"`.
Now install it:

```console
$ dbc install bigquery
[✓] searching
[✓] downloading
[✓] installing
[✓] verifying signature

Installed bigquery 1.0.0 to /Users/user/Library/Application Support/ADBC/Drivers
```

The BigQuery ADBC driver is now installed and usable by any driver manager.

For more information on on how to find drivers, see the [Finding Drivers](../guides/finding_drivers.md) guide.

## Installing a Driver Manager

To load any driver you install with dbc, you'll need an ADBC driver manager.
Let's install the driver manager for Python.

If during [installation](./installation.md), you installed dbc into a virtual environment, you can re-use that virtual environment for this step.
Otherwise, create a new virtual environment and activate it:

```console
$ python -m venv .venv
$ source .venv/bin/activate
```

!!! note inline end "Learning More"

    If you're interested in learning more about what a driver manager is, refer to the [Driver Manager concept guide](../concepts/driver_manager.md) or the more detailed [ADBC Driver Manager documentation](https://arrow.apache.org/adbc/current/format/how_manager.html).

Now, install the [`adbc_driver_manager`](https://pypi.org/project/adbc-driver-manager/) and [`pyarrow`](https://pypi.org/project/pyarrow/) packages:

```console
$ pip install adbc_driver_manager pyarrow
```

You're now ready to load the BigQuery driver and run some queries.

## Loading & Using a Driver

The `adbc_driver_manager` package provides a high-level [DBAPI-style](https://peps.python.org/pep-0249/) interface that may be familiar to you if you've connected to databases using Python before.

Import it like this:

```pycon
>>> from adbc_driver_manager import dbapi
```

We can now load the driver and pass in options with `dbapi.connect`.

We pass the name of the driver we just installed (`"bigquery"`) and, for options, we need to specify the `project_id` and `dataset_id`. `project_id` will be whatever you used in [Setup](#setup):

```pycon
>>> con = dbapi.connect(
...     driver="bigquery",
...     db_kwargs={
...         "adbc.bigquery.sql.project_id": "dbc-docs-first-steps",
...         "adbc.bigquery.sql.dataset_id": "bigquery-public-data",
...     },
... )
```

Next, we create a cursor:

```pycon
>>> cursor = con.cursor()
```

The query you'll run is from the [NYC Street Trees](https://console.cloud.google.com/marketplace/product/city-of-new-york/nyc-tree-census) dataset and the query will show the most common tree species and how healthy each species group is.

With our cursor, we can execute the following query:

```pycon
>>> cursor.execute("""
... SELECT
...     spc_latin,
...     spc_common,
...     COUNT(*) AS count,
...     ROUND(COUNTIF(health="Good")/COUNT(*)*100) AS healthy_pct
... FROM
...     `bigquery-public-data.new_york.tree_census_2015`
... WHERE
...     status="Alive"
... GROUP BY
...     spc_latin,
...     spc_common
... ORDER BY
...     count DESC
...""")
```

To get the data out of the query, we run:

```pycon
>>> tbl = cursor.fetch_arrow_table()
>>> tbl
pyarrow.Table
spc_latin: string
spc_common: string
count: int64
healthy_pct: double
----
spc_latin: [["Platanus x acerifolia","Gleditsia triacanthos var. inermis","Pyrus calleryana","Quercus palustris","Acer platanoides",...,"Pinus rigida","Maclura pomifera","Pinus sylvestris","Pinus virginiana",""]]
spc_common: [["London planetree","honeylocust","Callery pear","pin oak","Norway maple",...,"pitch pine","Osage-orange","Scots pine","Virginia pine",""]]
count: [[87014,64263,58931,53185,34189,...,33,29,25,10,5]]
healthy_pct: [[84,85,82,86,62,...,100,90,84,80,80]]
```

ADBC always returns query results in Arrow format so fetching the result as a PyArrow Table is a low-overhead operation.
However, the above display isn't the easiest to read and we might want to analyze our result using another package.

If we install [Polars](https://pola.rs) (`pip install polars`), we can use it to work with the result we just got:

```pycon
>>> import polars as pl
>>> df = pl.DataFrame(tbl)
>>> df
shape: (133, 4)
┌─────────────────────────────────┬──────────────────┬───────┬─────────────┐
│ spc_latin                       ┆ spc_common       ┆ count ┆ healthy_pct │
│ ---                             ┆ ---              ┆ ---   ┆ ---         │
│ str                             ┆ str              ┆ i64   ┆ f64         │
╞═════════════════════════════════╪══════════════════╪═══════╪═════════════╡
│ Platanus x acerifolia           ┆ London planetree ┆ 87014 ┆ 84.0        │
│ Gleditsia triacanthos var. ine… ┆ honeylocust      ┆ 64263 ┆ 85.0        │
│ Pyrus calleryana                ┆ Callery pear     ┆ 58931 ┆ 82.0        │
│ Quercus palustris               ┆ pin oak          ┆ 53185 ┆ 86.0        │
│ Acer platanoides                ┆ Norway maple     ┆ 34189 ┆ 62.0        │
│ …                               ┆ …                ┆ …     ┆ …           │
│ Pinus rigida                    ┆ pitch pine       ┆ 33    ┆ 100.0       │
│ Maclura pomifera                ┆ Osage-orange     ┆ 29    ┆ 90.0        │
│ Pinus sylvestris                ┆ Scots pine       ┆ 25    ┆ 84.0        │
│ Pinus virginiana                ┆ Virginia pine    ┆ 10    ┆ 80.0        │
│                                 ┆                  ┆ 5     ┆ 80.0        │
└─────────────────────────────────┴──────────────────┴───────┴─────────────┘
```

Much better.

Finally, now that we have our result saved as a Polars DataFrame, it's important to clean up after ourselves.
The `adbc_driver_manager` uses [context managers](https://docs.python.org/3/library/stdtypes.html#typecontextmanager) (`with` statements) to ensure resources are cleaned up automatically but, for the purpose of presentation here, we didn't use them.
To clean up, all we need to run is:

```pycon
>>> cursor.close()
>>> con.close()
```

Here's the entire code we just ran through as a single code block:

```python
from adbc_driver_manager import dbapi
import polars as pl

with dbapi.connect(
    driver="bigquery",
    db_kwargs={
        "adbc.bigquery.sql.project_id": "dbc-docs-first-steps",
        "adbc.bigquery.sql.dataset_id": "bigquery-public-data",
    },
) as con, con.cursor() as cursor:
    cursor.execute("""
    SELECT
      spc_latin,
      spc_common,
      COUNT(*) AS count,
      ROUND(COUNTIF(health="Good")/COUNT(*)*100) AS healthy_pct
    FROM
      `bigquery-public-data.new_york.tree_census_2015`
    WHERE
      status="Alive"
    GROUP BY
      spc_latin,
      spc_common
    ORDER BY
      count DESC"""
    )
    tbl = cursor.fetch_arrow_table()
    print(tbl)

    df = pl.DataFrame(tbl)
    print(df)
```

## Next Steps

Now you've run through a complete example of the process outlined at the start of the page:

1. Installing an ADBC driver with `dbc install bigquery`
2. Loading the driver with the [`adbc_driver_manager`](https://pypi.org/project/adbc-driver-manager/) package
3. Using the driver to run a query and return the result in Arrow format

As mentioned above, the process will be similar for any driver so hopefully you can adapt the steps here to another database.

dbc can do a lot more than just install drivers. If you're interested in learning everything dbc can do, visit the [dbc CLI](../reference/cli.md) reference.
