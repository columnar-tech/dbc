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

# Installing a Driver Manager

In order to use any of the drivers you [install](./installing.md) with dbc, you need to install a [driver manager](../concepts/driver_manager.md) for your language of choice.

The best place to find detailed information on driver manager installation is always the [ADBC docs](https://arrow.apache.org/adbc/) but we've included concise steps for a variety of languages here for convenience:

=== "Python"

    === "uv"

        ```console
        $ uv pip install adbc_driver_manager pyarrow
        ```

    === "pip"

        ```console
        $ pip install adbc_driver_manager pyarrow
        ```

    === "conda"

        ```console
        $ conda install -c conda-forge adbc-driver-manager pyarrow
        ```

=== "R"

    ```r
    install.packages("adbcdrivermanager")
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
        $ conda install -c conda-forge libadbc-driver-manager
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

    To use the Java ADBC driver manager in a Maven project, add the driver manager and JNI driver packages:

    ```xml
    <dependency>
        <groupId>org.apache.arrow.adbc</groupId>
        <artifactId>adbc-driver-manager</artifactId>
        <version>${adbc.version}</version>
    </dependency>
    <dependency>
        <groupId>org.apache.arrow.adbc</groupId>
        <artifactId>adbc-driver-jni</artifactId>
        <version>${adbc.version}</version>
    </dependency>
    ```

    Note that with the above you'll also need to set an `adbc.version` property to an appropriate version.
