# Copyright (c) 2025 Columnar Technologies.  All rights reserved.

# /// script
# requires-python = ">=3.5"
# dependencies = [
#     "wheel",
# ]
# ///
#
# create_wheels.py
#
# Adapted from https://github.com/ziglang/zig-pypi/blob/main/make_wheels.py
#
# Examples:
#
# Create wheels for all platforms for binary version 0.1:
#
# ```
# uv run python create_wheels.py --binary_version 0.1
# ```
#
# Create wheels for only Linux amd64 for binary version 0.1:
#
# ```
# uv run python create_wheels.py --binary_version 0.1 --platform linux-amd64
# ```
#
# Create wheels for only all amd64 platforms for binary version 0.1:
#
# ```
# uv run python create_wheels.py --binary_version 0.1 --platform amd64
# ```
#
# Use a different wheel version than binary version:
#
# ```
# uv run python create_wheels.py --binary_version 0.1 --wheel_version 0.1.1
# ```

import argparse
from email.message import EmailMessage
import hashlib
import io
import os
import urllib
import urllib.request
import json
from wheel.wheelfile import WheelFile
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo
import tarfile
from typing import List


GITHUB_ORG = "columnar-tech"
GITHUB_REPO = "dbc"
PACKAGE_NAME = "dbc"
PACKAGE_SUMMARY = "dbc is a tool for installing ADBC drivers"

# Where we write wheels to
OUT_DIR = "./dist"

# Map Golang GOOS and GOARCH to Python packaging platforms
PLATFORMS_MAP = {
    'windows-amd64': 'win_amd64',
    'windows-arm64': 'win_arm64',
    'windows-x86':    'win32',
    'darwin-amd64':   'macosx_12_0_x86_64',
    'darwin-arm64':   'macosx_12_0_arm64',
    'linux-x86':     'manylinux_2_12_i686.manylinux2010_i686',
    'linux-arm64':
        'manylinux_2_17_aarch64.manylinux2014_aarch64',
    'linux-armv7a':   'manylinux_2_17_armv7l.manylinux2014_armv7l',
    'linux-powerpc64le':  'manylinux_2_17_ppc64le.manylinux2014_ppc64le',
    'linux-s390x':     'manylinux_2_17_s390x.manylinux2014_s390x',
    'linux-riscv64':   'manylinux_2_31_riscv64',
}


def get_github_release(repo_owner, repo_name, release_tag):
    """
    Get release and asset information for a release by tag.

    Args:
        repo_owner (str): The owner of the GitHub repository.
        repo_name (str): The name of the GitHub repository.
        release_tag (str): The release tag to fetch.

    Returns:
        dict: A dictionary containing release information and asset details.
    """

    url = f"https://api.github.com/repos/{repo_owner}/{repo_name}/releases/tags/{release_tag}"

    try:
        with urllib.request.urlopen(url) as response:
            data = json.loads(response.read().decode())

        release_info = {
            "tag_name": data["tag_name"],
            "name": data["name"],
            "published_at": data["published_at"],
            "assets": [],  # Filled in next...
        }

        for asset in data["assets"]:
            release_info["assets"].append(
                {
                    "name": asset["name"],
                    "download_url": asset["browser_download_url"],
                    "digest": asset["digest"],
                }
            )

        return release_info
    except Exception as e:
        print(f"Error fetching release data: {e}")
        return None

def iter_archive_contents(archive):
    magic = archive[:4]
    if magic[:3] == b"\x1F\x8B\x08":
        with tarfile.open(mode="r|gz", fileobj=io.BytesIO(archive)) as tar:
            for entry in tar:
                if entry.isreg():
                    yield entry.name, entry.mode | (1 << 15), tar.extractfile(entry).read()
    elif magic[:4] == b"PK\x03\x04":
        with ZipFile(io.BytesIO(archive)) as zip_file:
            for entry in zip_file.infolist():
                if not entry.is_dir():
                    yield entry.filename, entry.external_attr >> 16, zip_file.read(entry)
    else:
        raise RuntimeError("Unsupported archive format")

def make_message(headers, payload=None):
    msg = EmailMessage()
    for name, value in headers:
        if isinstance(value, list):
            for value_part in value:
                msg[name] = value_part
        else:
            msg[name] = value
    if payload:
        msg.set_payload(payload)
    return msg


class ReproducibleWheelFile(WheelFile):
    def writestr(self, zinfo_or_arcname, data, *args, **kwargs):
        if isinstance(zinfo_or_arcname, ZipInfo):
            zinfo = zinfo_or_arcname
        else:
            assert isinstance(zinfo_or_arcname, str)
            zinfo = ZipInfo(zinfo_or_arcname)
            zinfo.file_size = len(data)
            zinfo.external_attr = 0o0644 << 16
            if zinfo_or_arcname.endswith(".dist-info/RECORD"):
                zinfo.external_attr = 0o0664 << 16

        zinfo.compress_type = ZIP_DEFLATED
        super().writestr(zinfo, data, *args, **kwargs)


def write_wheel_file(filename, contents):
    with ReproducibleWheelFile(filename, "w") as wheel:
        for member_info, member_source in contents.items():
            wheel.writestr(member_info, bytes(member_source))
    return filename


def write_wheel(out_dir, *, name, version, tag, metadata, description, contents):
    if not os.path.exists(out_dir):
        os.mkdir(out_dir)

    wheel_name = f"{name}-{version}-{tag}.whl"
    dist_info = f"{name}-{version}.dist-info"
    filtered_metadata = []
    for header, value in metadata:
        filtered_metadata.append((header, value))

    return write_wheel_file(
        os.path.join(out_dir, wheel_name),
        {
            **contents,
            f"{dist_info}/entry_points.txt": make_message([],
               '[console_scripts]\ndbc = dbc.__main__:dummy'),
            f"{dist_info}/METADATA": make_message(
                [
                    ("Metadata-Version", "2.4"),
                    ("Name", name),
                    ("Version", version),
                    *filtered_metadata,
                ],
                description,
            ),
            f"{dist_info}/WHEEL": make_message(
                [
                    ("Wheel-Version", "1.0"),
                    ("Generator", f"{PACKAGE_NAME} create_wheels.py"),
                    ("Root-Is-Purelib", "false"),
                    ("Tag", tag),
                ]
            ),
        },
    )


def create_wheel(version: str, platform: str, archive: bytes):
    contents = {}
    contents[f"{PACKAGE_NAME}/__init__.py"] = b""

    # Handle license files, ensuring we don't miss any
    required_license_paths = [
        "LICENSE",
    ]
    license_files = {}
    found_license_files = set()

    # Scan the binary archive and extract what we need from it
    bin_prefix = PACKAGE_NAME[:-3]
    bin_found = False

    # Copy all files from the source zip into the wheel
    for entry_name, entry_mode, entry_data in iter_archive_contents(archive):
        if not entry_name:
            continue

        zip_info = ZipInfo(f"{PACKAGE_NAME}/{entry_name}")
        zip_info.external_attr = (entry_mode & 0xFFFF) << 16
        contents[zip_info] = entry_data

        # Collect license files
        if entry_name in required_license_paths:
            license_files[entry_name] = entry_data
            found_license_files.add(entry_name)

        if entry_name.startswith(bin_prefix):
            bin_found = True
            contents[f"{PACKAGE_NAME}/__main__.py"] = f'''\
import os, sys
argv = [os.path.join(os.path.dirname(__file__), "{entry_name}"), *sys.argv[1:]]
if os.name == 'posix':
    os.execv(argv[0], argv)
else:
    import subprocess; sys.exit(subprocess.call(argv))

def dummy(): """Dummy function for an entrypoint. dbc is executed as a side effect of the import."""
'''.encode('ascii')

    if not bin_found:
        raise RuntimeError("No binary found in archive. Stopping now.")

    # Set the content of the PyPI README as the description
    with open(os.path.join(os.path.dirname(__file__), "README.pypi.md")) as f:
        description = f.read()

    # Add licenses we found
    dist_info = f"{PACKAGE_NAME}-{version}.dist-info"
    for license_path, license_data in license_files.items():
        contents[f"{dist_info}/licenses/{license_path}"] = license_data

    missing_licenses = set(required_license_paths) - found_license_files
    if missing_licenses:
        raise RuntimeError(f"Missing licenses: {missing_licenses}")

    path = write_wheel(
        OUT_DIR,
        name=f"{PACKAGE_NAME}",
        version=version,
        tag=f"py3-none-{platform}",
        metadata=[
            (
                "Summary",
                PACKAGE_SUMMARY,
            ),
            ("Description-Content-Type", "text/markdown"),
            ("License-Expression", "Apache-2.0"),
            ("License-File", "LICENSE"),
            ("Requires-Python", "~=3.5"),
        ],
        description=description,
        contents=contents,
    )

    print(f"Created wheel {path}.")


def create_wheels(platforms: List[str], binary_version: str, wheel_version: str):
    release_info = get_github_release(GITHUB_ORG, GITHUB_REPO, binary_version)

    if release_info is None:
        raise Exception(f"Release for version {binary_version} not found!")

    for asset in release_info["assets"]:
        asset_name = asset["name"]
        tokens = asset_name.split("-")
        platform_os = tokens[1]
        platform_arch = tokens[2]

        # Skip any assets we didn't specify
        asset_platform = f"{platform_os}-{platform_arch}"
        if asset_platform not in platforms:
            print(
                f"Skipped {asset_name} because it wasn't in the provided list of platforms.  "
            )
            continue

        version = ".".join(tokens[3].split(".")[:-1])
        assert (
            version == binary_version
        ), f"Version mismatch: {version} != {binary_version}"
        archive_url = asset["download_url"]

        print(f"Creating wheel for asset: {asset_name}...")

        with urllib.request.urlopen(archive_url) as request:
            archive = request.read()

            # Verify hashes match
            actual_hash = hashlib.sha256(archive).hexdigest()
            expected_hash = asset["digest"].split(":")[1]
            if actual_hash != expected_hash:
                raise Exception(
                    f"Hash mismatch. Expected {expected_hash}, got {actual_hash}."
                )

        target_platform = PLATFORMS_MAP[f"{asset_platform}"]
        create_wheel(wheel_version, target_platform, archive)


def parse_platforms(platforms: str) -> List[str]:
    if platforms == "all":
        return list(PLATFORMS_MAP.keys())

    patterns = platforms.split(",")
    matched = []
    for platform in PLATFORMS_MAP.keys():
        for pattern in patterns:
            if platform == pattern:
                matched.append(platform)

            # Match either os or arch
            os, arch = platform.split("-")
            if pattern == os or pattern == arch:
                matched.append(platform)

    return matched


def parse_args() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--binary_version",
        help="Version of the binary to create wheels for. Should be a tag name.",
    )
    parser.add_argument(
        "--wheel_version",
        default="same",
        help="Version to give the wheels. Defaults to using the same version as the binary but can be overridden.",
    )
    parser.add_argument(
        "--platform",
        default="all",
        help="Platform(s) to create wheels for. Defaults to 'all' which creates wheels for all supported platforms. Platform strings follow the pattern <GOOS>-<GOARCH> and a full platform string can be provided or just GOOS or GOARCH (e.g., linux-amd64, linux, amd64.). Multiple values can be separated with a comma.",
    )
    parser.add_argument(
        "--archive",
        help="Path to a local archive to use instead of downloading from GitHub.",
    )
    return parser


def main():
    args = parse_args().parse_args()
    if args.archive:
        # If an archive is provided, use it
        with open(args.archive, "rb") as f:
            archive = f.read()

        target = PLATFORMS_MAP[f"{args.platform}"]
        create_wheel(args.binary_version, target, archive)
        return
    
    platforms = parse_platforms(args.platform)
    if len(platforms) <= 0:
        raise RuntimeError("No platforms provided. See usage with --help.")

    create_wheels(platforms, args.binary_version, args.wheel_version)


if __name__ == "__main__":
    main()
