# Installing a Driver Manager

In order to use any of the drivers you [install](./installing.md) with dbc, you need to install a [driver manager](../concepts/driver_manager.md) for your language of choice.

The best place to find detailed information on driver manager installation is always the [ADBC docs](https://arrow.apache.org/adbc/) but we've included concise steps for a variety of languages here for convenience:

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

    === "Bundler"

        ```console
        $ bundle add red-adbc
        ```

    === "gem"

        ```console
        $ gem install red-adbc
        ```

=== "Rust"

    ```console
    $ cargo add adbc_core adbc_driver_manager
    ```

=== "C++"

    === "conda"

        ```console
        $ conda install -c conda-forge adbc-driver-manager
        ```

    === "apt"

        ```sh
        $ # Set up the Apache Arrow APT repository
        $ sudo apt update
        $ sudo apt install -y -V ca-certificates lsb-release wget
        $ sudo wget https://apache.jfrog.io/artifactory/arrow/$(lsb_release --id --short | tr 'A-Z' 'a-z')/apache-arrow-apt-source-latest-$(lsb_release --codename --short).deb
        $ sudo apt install -y -V ./apache-arrow-apt-source-latest-$(lsb_release --codename --short).deb
        $ rm ./apache-arrow-apt-source-latest-$(lsb_release --codename --short).deb
        $ sudo apt update
        $ # Install libadbc-driver-manager-dev
        $ sudo apt install libadbc-driver-manager-dev
        ```

    === "dnf"

        ```sh
        $ # Set up the Apache Arrow Yum repository
        $ sudo dnf install -y epel-release || sudo dnf install -y oracle-epel-release-el$(cut -d: -f5 /etc/system-release-cpe | cut -d. -f1) || sudo $ dnf install -y https://dl.fedoraproject.org/pub/epel/epel-release-latest-$(cut -d: -f5 /etc/system-release-cpe | cut -d. -f1).noarch.rpm
        $ sudo dnf install -y https://apache.jfrog.io/artifactory/arrow/almalinux/$(cut -d: -f5 /etc/system-release-cpe | cut -d. -f1)/apache-arrow-release-latest.rpm
        $ sudo dnf config-manager --set-enabled epel || :
        $ sudo dnf config-manager --set-enabled powertools || :
        $ sudo dnf config-manager --set-enabled crb || :
        $ sudo dnf config-manager --set-enabled ol$(cut -d: -f5 /etc/system-release-cpe | cut -d. -f1)_codeready_builder || :
        $ sudo dnf config-manager --set-enabled codeready-builder-for-rhel-$(cut -d: -f5 /etc/system-release-cpe | cut -d. -f1)-rhui-rpms || :
        $ sudo subscription-manager repos --enable codeready-builder-for-rhel-$(cut -d: -f5 /etc/system-release-cpe | cut -d. -f1)-$(arch)-rpms || :
        $ # Install libadbc-driver-manager-devel
        $ sudo dnf install adbc-driver-manager-devel

        ```

=== "Java"

    To add the Java ADBC driver manager to a Maven project, add the following dependency:

    ```xml
    <dependency>
        <groupId>org.apache.arrow.adbc</groupId>
        <artifactId>adbc-driver-manager</artifactId>
        <version>${adbc.version}</version>
    </dependency>
    ```
