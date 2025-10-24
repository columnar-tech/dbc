#!/usr/bin/env python3

# Copyright 2025 Columnar Technologies Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.


import argparse
import tarfile
import yaml
from pathlib import Path

from faker import Faker

PLATFORMS = [
    "linux_amd64",
    "linux_arm64",
    "macos_amd64",
    "macos_arm64",
    "windows_amd64",
    "windows_arm64",
]


def main():
    parser = argparse.ArgumentParser(
        description="Create a local driver registry with fake data"
    )
    parser.add_argument(
        "--output-dir",
        "-o",
        default="cdn-dev",
        help="Output directory for the driver registry (default: cdn-dev)",
    )
    parser.add_argument(
        "--num-drivers",
        "-d",
        type=int,
        default=10,
        help="Number of fake drivers to create (default: 10)",
    )
    parser.add_argument(
        "--num-versions",
        "-v",
        type=int,
        default=10,
        help="Number of versions per driver (default: 10)",
    )

    args = parser.parse_args()

    create_driver_index(args.output_dir, args.num_drivers, args.num_versions)
    print(f"Driver registry root created in '{args.output_dir}'")


def create_driver_index(output_dir: str, num_drivers: int, num_versions: int):
    fake = Faker()

    # Create output directory
    output_path = Path(output_dir)
    output_path.mkdir(exist_ok=True)

    # Generate drivers data
    drivers = []

    for i in range(num_drivers):
        # Generate a unique driver name
        driver_name = fake.unique.word().lower()

        driver_data = {
            "name": f"{driver_name.title()}",
            "description": f"Test Driver for {driver_name.title()}",
            "license": "Apache-2.0",
            "path": driver_name,
            "urls": [f"https://example.org/{driver_name}"],
            "pkginfo": [],
        }

        # Create driver directory
        driver_dir = output_path / driver_name
        driver_dir.mkdir(exist_ok=True)

        # Generate versions for this driver
        for version_num in range(1, num_versions + 1):
            version = f"v0.{version_num}.0"
            version_dir = driver_dir / version
            version_dir.mkdir(exist_ok=True)

            # Create packages for each platform
            packages = []
            for platform in PLATFORMS:
                filename = f"{driver_name}_{platform}_{version}.tar.gz"
                file_path = version_dir / filename

                # Create empty tar.gz file
                create_empty_tarball(file_path)

                packages.append(
                    {"platform": platform, "url": f"{driver_name}/{version}/{filename}"}
                )

            # Add version info to driver
            driver_data["pkginfo"].append({"version": version, "packages": packages})

        drivers.append(driver_data)

    # Create manifest.yaml
    manifest = {"drivers": drivers}
    manifest_path = output_path / "index.yaml"

    with open(manifest_path, "w") as f:
        yaml.dump(manifest, f, default_flow_style=False, sort_keys=False)


def create_empty_tarball(file_path: Path):
    with tarfile.open(file_path, "w:gz") as tar:
        pass  # empty


if __name__ == "__main__":
    main()
