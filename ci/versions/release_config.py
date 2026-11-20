#!/usr/bin/env python3
"""Read release identity and tags from the final CSV row, checked against versions.txt."""

import argparse
import csv
import re
import sys

from tags_table import CSV_PATH, REPO_DIR, read_releases


def release_config():
    values = {}
    numeric = r"(?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*)){2}"
    for line in (REPO_DIR / "ci/versions/versions.txt").read_text().splitlines():
        if not line or line.startswith("#"):
            continue
        key, separator, value = line.partition("=")
        pattern = numeric + (r"(?:-[A-Za-z0-9_][A-Za-z0-9_.-]*)?" if key == "APP_VERSION" else "")
        if separator != "=" or key not in {"APP_VERSION", "LEGO_VERSION", "ALPINE_VERSION"} or not re.fullmatch(pattern, value):
            raise ValueError("Invalid entry in ci/versions/versions.txt")
        if key in values:
            raise ValueError(f"Duplicate version key: {key}")
        values[key] = value
    if set(values) != {"APP_VERSION", "LEGO_VERSION", "ALPINE_VERSION"}:
        raise ValueError("Missing version keys in ci/versions/versions.txt")

    latest = read_releases(CSV_PATH)[-1]
    if not latest.field("App URL"):
        raise ValueError("The current release must have an App URL; empty URLs identify unpublished branches.")
    app = re.sub(r"^[vV]", "", latest.app_full)
    configured_app = values["APP_VERSION"]
    if app != configured_app:
        raise ValueError("The final CSV application version differs from APP_VERSION in ci/versions/versions.txt")
    if latest.field("Docker OS").lower() != "alpine" or latest.field("Docker OS Version") != values["ALPINE_VERSION"]:
        raise ValueError("The final CSV Alpine version differs from ALPINE_VERSION in ci/versions/versions.txt")
    if re.sub(r"^[vV]", "", latest.field("Lego Version")) != values["LEGO_VERSION"] or latest.field("Lego Suffix"):
        raise ValueError("The final CSV lego version differs from LEGO_VERSION in ci/versions/versions.txt")
    values["APP_VERSION"] = app
    values["APP_TAG"] = latest.app_full
    return values, latest.tags


def tag_candidates(values, tags):
    app = values["APP_VERSION"]
    candidates = [values["APP_TAG"], app, "v" + app, "V" + app, *(tags[i] for i in (0, 1, 3, 4))]
    return tuple(dict.fromkeys(tag for tag in candidates if len(tag) <= 128))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag-candidates", action="store_true")
    args = parser.parse_args()
    try:
        values, tags = release_config()
        if args.tag_candidates:
            print("\n".join(tag_candidates(values, tags)))
        else:
            print("\n".join(f"{key}\t{value}" for key, value in values.items()))
            print("\n".join(f"TAG\t{tag}" for tag in tags))
    except (OSError, ValueError, csv.Error) as error:
        print(f"Release configuration error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
