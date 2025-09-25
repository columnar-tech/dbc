@echo on

dir
python -m venv .venv
call ".venv\Scripts\activate.bat"
pip install adbc_driver_manager

set PATH=%CD%;%PATH%
dbc install duckdb

python -c "from adbc_driver_manager import dbapi; dbapi.connect(driver=\"duckdb\");"
