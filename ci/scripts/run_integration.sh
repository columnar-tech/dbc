#!/bin/sh

set -eux

python -m venv .venv

if [ -f ".venv/bin/activate" ]; then
  . ".venv/bin/activate"
else
  . ".venv/Scripts/activate"
fi

pip install adbc_driver_manager

./dbc install duckdb

python -c "from adbc_driver_manager import dbapi; dbapi.connect(driver=\"duckdb\");"
