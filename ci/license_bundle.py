#!/usr/bin/env python3
"""Install a BuildKit text-only license export without retaining any binaries."""

import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import shutil
import sys
import tempfile

HASH = re.compile(r"[a-f0-9]{64}")


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON key: " + key)
        result[key] = value
    return result


def validate(directory):
    directory = Path(directory)
    if directory.is_symlink():
        raise ValueError("license export must be a directory, not a link")
    manifest = directory / "bundle.json"
    if manifest.is_symlink() or not manifest.is_file() or manifest.stat().st_size > 64 << 20:
        raise ValueError("missing or oversized bundle.json")
    data = json.loads(manifest.read_text(), object_pairs_hook=unique_object)
    if set(data) != {"schema", "components", "data_files"} or data["schema"] != 1:
        raise ValueError("unknown license bundle schema")
    if not data["components"] or not data["data_files"]:
        raise ValueError("incomplete license export")
    expected = {"bundle.json"}
    components = set()
    for component in data["components"]:
        if component["id"] in components or not component["origin"] or not component["documents"]:
            raise ValueError("duplicate or incomplete component")
        components.add(component["id"])
        seen = set()
        for document in component["documents"]:
            source = document["source"]
            path = PurePosixPath(source)
            if not source or path.is_absolute() or ".." in path.parts or "\\" in source or str(path) != source or source in seen:
                raise ValueError("invalid original notice path")
            seen.add(source)
            digest = document["sha256"]
            if not isinstance(digest, str) or not HASH.fullmatch(digest):
                raise ValueError("invalid notice checksum")
            name = "texts/" + digest + ".txt"
            expected.add(name)
            notice = directory / name
            if notice.is_symlink() or not notice.is_file() or not 0 < notice.stat().st_size <= 16 << 20:
                raise ValueError("missing or oversized notice: " + name)
            if hashlib.sha256(notice.read_bytes()).hexdigest() != digest:
                raise ValueError("notice checksum changed: " + name)
    for path in directory.rglob("*"):
        relative = path.relative_to(directory).as_posix()
        if path.is_symlink() or (path.is_dir() and relative != "texts"):
            raise ValueError("unexpected license export path: " + relative)
        if not path.is_dir() and (not path.is_file() or relative not in expected or path.stat().st_mode & 0o111):
            raise ValueError("export contains an unexpected file or executable: " + relative)
    return data


def install(source, destination):
    source, destination = Path(source), Path(destination)
    data = validate(source)
    if destination.is_symlink():
        raise ValueError("license destination must not be a symlink")
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".license-install-", dir=destination.parent) as temporary:
        temporary = Path(temporary)
        staged, previous = temporary / "new", temporary / "previous"
        shutil.copytree(source, staged)
        readme = destination / "README.md"
        if readme.is_file() and not readme.is_symlink():
            shutil.copyfile(readme, staged / "README.md")
        existed = destination.exists()
        if existed:
            destination.rename(previous)
        try:
            staged.rename(destination)
        except OSError:
            if existed:
                previous.rename(destination)
            raise
    return len(data["components"])


def main():
    if len(sys.argv) != 4 or sys.argv[1] != "install":
        raise ValueError("Usage: python3 ci/license_bundle.py install EXPORT DESTINATION")
    count = install(sys.argv[2], sys.argv[3])
    print(f"Saved {count} third-party components and original notice texts in {sys.argv[3]}")
    print("No third-party executables or source archives were exported.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, TypeError, OSError) as error:
        print("License bundle error: " + str(error), file=sys.stderr)
        sys.exit(1)
