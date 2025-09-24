#!/bin/sh

# set up venv
python -m venv .venv
. ".venv/bin/activate"
pip install adbc_driver_manager

# install duckdb driver
./dbc install duckdb

# test with driver manager
python -c "from adbc_driver_manager import dbapi; dbapi.connect(driver=\"duckdb\");"
