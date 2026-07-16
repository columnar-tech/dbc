#!/usr/bin/env python3
# Copyright 2026 Columnar Technologies Inc.
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

"""
Sync dbc command output in documentation.

Finds every block delimited by:

    <!-- dbc-output: ARGS -->
    ```console
    $ dbc ARGS
    <live output>
    ```
    <!-- /dbc-output -->

markers in docs/ and replaces the block content by running `dbc ARGS` live.

The script requires a clean environment — no drivers may be installed when it
runs. This ensures that commands like `dbc search` reflect only what is
available in the registry and are not affected by locally installed state.
The script checks this upfront via `dbc list --json` and exits with an error
if any drivers are found.

Usage:
    python scripts/sync_docs.py          # Update docs in-place
    python scripts/sync_docs.py --check  # Exit 1 if any blocks are stale
"""

import argparse
import json
import re
import shlex
import shutil
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DOCS_DIR = REPO_ROOT / "docs"

# Matches a complete annotated block including its surrounding markers.
BLOCK_RE = re.compile(
    r"<!-- dbc-output: (?P<args>[^\n]+?) -->\n"
    r"```console\n"
    r"(?P<content>.*?)"
    r"```\n"
    r"<!-- /dbc-output -->",
    re.DOTALL,
)


def check_clean_environment() -> str | None:
    """
    Return an error message if any drivers are installed, None if the
    environment is clean.
    """
    result = subprocess.run(
        ["dbc", "list", "--json"],
        capture_output=True,
        text=True,
        timeout=30,
    )
    if result.returncode != 0:
        return f"`dbc list --json` failed: {result.stderr.strip() or f'exited {result.returncode}'}"
    data = json.loads(result.stdout)
    drivers = data.get("payload", {}).get("drivers", [])
    if drivers:
        names = ", ".join(d["driver"] for d in drivers)
        return (
            f"found {len(drivers)} installed driver(s): {names}\n"
            "Uninstall all drivers before running this script to avoid "
            "polluting the output."
        )
    return None


def run_dbc(args_str: str) -> str:
    """Run `dbc ARGS` and return stdout. Raises RuntimeError on failure."""
    args = shlex.split(args_str)
    try:
        result = subprocess.run(
            ["dbc"] + args,
            capture_output=True,
            text=True,
            timeout=30,
        )
    except FileNotFoundError:
        raise RuntimeError("`dbc` not found in PATH — install with: pip install dbc")
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or f"exited {result.returncode}")
    return "\n".join(line.rstrip() for line in result.stdout.splitlines())


def make_block(args_str: str, output: str) -> str:
    """Build the full annotated block string from fresh command output."""
    output = output.rstrip("\n") + "\n"
    return (
        f"<!-- dbc-output: {args_str} -->\n"
        f"```console\n"
        f"$ dbc {args_str}\n"
        f"{output}"
        f"```\n"
        f"<!-- /dbc-output -->"
    )


def sync_file(path: Path, check: bool) -> tuple[bool, list[str]]:
    """
    Sync all annotated blocks in a single file.
    Returns (changed, errors). When check=True the file is never written.
    """
    original = path.read_text(encoding="utf-8")
    changed = False
    errors: list[str] = []

    def replacer(m: re.Match) -> str:
        nonlocal changed
        args_str = m.group("args")
        try:
            output = run_dbc(args_str)
        except Exception as exc:
            errors.append(f"{path.relative_to(REPO_ROOT)}: `dbc {args_str}`: {exc}")
            return m.group(0)

        new_block = make_block(args_str, output)
        if new_block != m.group(0):
            changed = True
        return new_block

    updated = BLOCK_RE.sub(replacer, original)

    if changed and not check:
        path.write_text(updated, encoding="utf-8")

    return changed, errors


def main() -> int:
    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="Exit non-zero if any blocks are out of date (do not modify files)",
    )
    opts = parser.parse_args()

    if not shutil.which("dbc"):
        print(
            "error: `dbc` not found in PATH — install with: uv tool install dbc",
            file=sys.stderr,
        )
        return 1

    env_error = check_clean_environment()
    if env_error:
        print(f"error: {env_error}", file=sys.stderr)
        return 1

    doc_files = sorted(DOCS_DIR.rglob("*.md"))
    stale: list[Path] = []
    all_errors: list[str] = []

    for path in doc_files:
        if "<!-- dbc-output:" not in path.read_text(encoding="utf-8"):
            continue
        changed, errors = sync_file(path, check=opts.check)
        all_errors.extend(errors)
        if changed:
            stale.append(path)
            verb = "stale" if opts.check else "updated"
            print(f"{verb}: {path.relative_to(REPO_ROOT)}")

    if all_errors:
        for err in all_errors:
            print(f"error: {err}", file=sys.stderr)
        return 1

    if opts.check and stale:
        print(
            f"\n{len(stale)} file(s) have stale dbc output. "
            "Run `python scripts/sync_docs.py` to update.",
            file=sys.stderr,
        )
        return 1

    if not stale:
        print("All annotated blocks are up to date.")

    return 0


if __name__ == "__main__":
    sys.exit(main())
