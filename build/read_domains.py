#!/usr/bin/env python3
"""Read and validate the certificate groups before starting any ACME request."""

import re
import sys
from pathlib import Path

import yaml


FOLDER = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9_-]{0,99}")
LABEL = r"[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?"
DOMAIN = re.compile(rf"(?:\*\.)?{LABEL}(?:\.{LABEL})+")


class DomainsLoader(yaml.SafeLoader):
    def construct_mapping(self, node, deep=False):
        mapping = {}
        for key_node, value_node in node.value:
            key = self.construct_object(key_node, deep=True)
            if not isinstance(key, str) or not FOLDER.fullmatch(key):
                raise ValueError(f"invalid certificate folder: {key!r}")
            if key in mapping:
                raise ValueError(f"duplicate certificate folder: {key}")
            mapping[key] = self.construct_object(value_node, deep=deep)
        return mapping


def read_groups(path):
    with Path(path).open(encoding="utf-8") as handle:
        groups = yaml.load(handle, Loader=DomainsLoader)
    if not isinstance(groups, dict) or not groups:
        raise ValueError("expected a nonempty mapping of certificate folders to domain lists")

    for name, domains in groups.items():
        if not isinstance(domains, list) or not domains:
            raise ValueError(f"{name}: expected a nonempty domain list")
        if len(domains) > 100:
            raise ValueError(f"{name}: {len(domains)} identifiers exceed the 100-name limit")

        seen = set()
        for domain in domains:
            if (
                not isinstance(domain, str)
                or len(domain) > 253
                or not DOMAIN.fullmatch(domain)
                or domain.rsplit(".", 1)[-1].isdigit()
            ):
                raise ValueError(f"{name}: invalid DNS name: {domain!r}")
            if domain.lower() in seen:
                raise ValueError(f"{name}: duplicate DNS name: {domain}")
            seen.add(domain.lower())
    return groups


def main(path, selected):
    try:
        groups = read_groups(path)
        unknown = dict.fromkeys(name for name in selected if name not in groups)
        if unknown:
            raise ValueError(f"unknown certificate folder(s): {', '.join(unknown)}")
    except (OSError, ValueError, RecursionError, yaml.YAMLError) as exc:
        print(f"Error in {path}: {exc}", file=sys.stderr)
        return 2

    for name, domains in groups.items():
        if not selected or name in selected:
            # Validated names and domains contain no commas, tabs or newlines.
            print(name, ",".join(domains), sep="\t")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1], sys.argv[2:]))
