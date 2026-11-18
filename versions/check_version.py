#!/usr/bin/env python3
"""Verify one committed tag table against the release about to be published."""

import argparse
import csv
import re
import subprocess
import sys

from tags_table import (
    CSV_PATH,
    DOCKER_IMAGE,
    REPO_DIR,
    complete_table,
    read_releases,
    render_document,
    short_table,
)


def expected_tags() -> tuple[str, ...]:
    result = subprocess.run(
        ["bash", "ci/publish-image.sh", "--print-tags"],
        cwd=REPO_DIR,
        text=True,
        capture_output=True,
    )
    if result.returncode:
        raise ValueError(result.stderr.strip() or "Cannot read the release tags from versions/versions.txt.")
    prefix = f"{DOCKER_IMAGE}:"
    refs = result.stdout.splitlines()
    if len(refs) != 9 or any(not ref.startswith(prefix) for ref in refs):
        raise ValueError("The publisher must provide nine Docker tags.")
    return tuple(ref.removeprefix(prefix) for ref in refs)


def check_ref(ref: str, version: str) -> None:
    if not ref:
        return
    short_ref = ref.removeprefix("refs/heads/").removeprefix("refs/tags/")
    result = subprocess.run(
        ["bash", "ci/is-version-branch.sh", short_ref, "--print-version"],
        cwd=REPO_DIR,
        text=True,
        capture_output=True,
    )
    if result.returncode == 1:
        return  # A non-versioned ref uses the full APP_VERSION from versions/versions.txt.
    result.check_returncode()
    numbers = (part.lstrip("0") or "0" for part in result.stdout.strip().split(".")[:3])
    branch_version = list(numbers)
    if version[1:].split(".")[:len(branch_version)] != branch_version:
        raise ValueError(f"{ref!r} does not match APP_VERSION={version[1:]} in versions/versions.txt.")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("document", choices=("DOCKERHUB.md", "README.md"))
    parser.add_argument("--ref", default="", help="Version branch or release tag to check")
    args = parser.parse_args()

    try:
        tags = expected_tags()
        version, = (tag for tag in tags if re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", tag))
        check_ref(args.ref, version)

        releases = read_releases(CSV_PATH)
        latest = releases[-1]
        if latest.app_full != version:
            raise ValueError(
                f"The final row of versions/versions.csv is {latest.app_full}. "
                f"Add {version} as the final row and run python3 versions/tags_table.py."
            )
        if latest.tags != tags:
            raise ValueError(
                f"The Docker tags for {version} in versions/versions.csv differ from versions/versions.txt. "
                "Check the Alpine, lego, and application versions and suffixes."
            )

        table = short_table(releases) if args.document == "DOCKERHUB.md" else complete_table(releases)
        current, updated = render_document(REPO_DIR / args.document, table)
        if current != updated:
            raise ValueError(
                f"{args.document} is missing the current generated table for {version}. "
                "Run python3 versions/tags_table.py and commit both Markdown files."
            )
        print(f"{args.document}: {version} and its Docker tag table verified.")
    except (OSError, ValueError, csv.Error, subprocess.CalledProcessError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
