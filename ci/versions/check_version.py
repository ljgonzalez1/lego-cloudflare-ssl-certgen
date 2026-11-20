#!/usr/bin/env python3
"""Verify one committed tag table against the release about to be published."""

import argparse
import csv
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
        raise ValueError(result.stderr.strip() or "Cannot read the release tags from ci/versions/versions.txt.")
    prefix = f"{DOCKER_IMAGE}:"
    refs = result.stdout.splitlines()
    if len(refs) not in (8, 9) or any(not ref.startswith(prefix) for ref in refs):
        raise ValueError("The publisher must provide eight or nine Docker tags.")
    return tuple(ref.removeprefix(prefix) for ref in refs)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("document", choices=("DOCKERHUB.md", "README.md"))
    # Kept for existing callers, but the Git ref never overrides the committed release identity.
    parser.add_argument("--ref", default="", help="Compatibility argument; ignored")
    args = parser.parse_args()

    try:
        tags = expected_tags()
        version = tags[5]  # Full application version, preserving prefix and optional CSV suffix.

        releases = read_releases(CSV_PATH)
        latest = releases[-1]
        if latest.app_full != version:
            raise ValueError(
                f"The final row of ci/versions/versions.csv is {latest.app_full}. "
                f"Add {version} as the final row and run python3 ci/versions/tags_table.py."
            )
        if latest.tags != tags:
            raise ValueError(
                f"The Docker tags for {version} in ci/versions/versions.csv differ from ci/versions/versions.txt. "
                "Check the Alpine, lego, and application versions and suffixes."
            )

        table = short_table(releases) if args.document == "DOCKERHUB.md" else complete_table(releases)
        current, updated = render_document(REPO_DIR / args.document, table)
        if current != updated:
            raise ValueError(
                f"{args.document} is missing the current generated table for {version}. "
                "Run python3 ci/versions/tags_table.py and commit both Markdown files."
            )
        print(f"{args.document}: {version} and its Docker tag table verified.")
    except (OSError, ValueError, csv.Error, subprocess.CalledProcessError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
