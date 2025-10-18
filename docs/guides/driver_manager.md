# Installing a Driver Manager

In order to use any of the drivers you [install](./installing.md) with dbc, you need to install a [driver manager](../concepts/driver_manager.md) for your language of choice.

The best place to find detailed information on driver manager installation is always the [ADBC docs](https://arrow.apache.org/adbc/) but we've included concise steps here for convenience:

=== "Python"

    === "uv"

        ```console
        $ uv pip install adbc_driver_manager pyarrow
        ```

    === "venv"

        ```console
        $ pip install adbc_driver_manager pyarrow
        ```

    === "conda"

        ```console
        $ conda install -c conda-forge adbc-driver-manager pyarrow
        ```

=== "R"

    ```r
    > install.packages("adbcdrivermanager")
    ```

=== "Go"

    ```console
    $ go get github.com/apache/arrow-adbc/go/adbc/drivermgr
    ```

=== "Ruby"

    === "bundle"

    ```console
    $ bundle add red-adbc
    ```

    === "gem"

    ```console
    $ gem install red-adbc
    ```

=== "Rust"

    ```console
    $ cargo add adbc...TODO
    ```

=== "C++"

    === "conda"

        ```console
        $ conda install -c conda-forge adbc-driver-manager pyarrow
        ```

    === "apt"

        ```sh
        $ TODO
        ```

    === "dnf"

        ```sh
        $ TODO
        ```

    CMake TODO

    ```sh
    TODO
    ```

=== "Java"

    TODO

    ```console
    $ TODO
    ```
