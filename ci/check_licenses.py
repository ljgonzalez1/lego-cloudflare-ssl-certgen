#!/usr/bin/env python3
"""Verify source notice copies and record the boundaries of supplied asset rights."""

import hashlib
import json
from pathlib import Path
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parent.parent


def main():
    project = (ROOT / "LICENSE").read_bytes()
    for name in ("build/licenses/project.txt", "build/licenses/project/LICENSE"):
        if (ROOT / name).read_bytes() != project:
            raise ValueError(f"Project license copy differs: {name}")
    for name, required in {
        "build/licenses/Apache-2.0.txt": (b"TERMS AND CONDITIONS", b"END OF TERMS AND CONDITIONS"),
        "build/licenses/MPL-2.0.txt": (b"Mozilla Public License", b"Exhibit B"),
    }.items():
        data = (ROOT / name).read_bytes()
        if not all(text in data for text in required):
            raise ValueError(f"Incomplete license text: {name}")
    pins = json.loads((ROOT / "licenses/badges/sources.json").read_text())
    if pins["license"] != "CC0-1.0":
        raise ValueError("Unexpected badge icon license")
    paths = {entry["path"] for entry in pins["files"]}
    required = {"licenses/badges/SimpleIcons-LICENSE.md", *("ci/badge-icons/" + name + ".svg" for name in ("docker", "github", "gitea", "go"))}
    if paths != required:
        raise ValueError("Incomplete badge icon/source notice inventory")
    for entry in pins["files"]:
        data = (ROOT / entry["path"]).read_bytes()
        blob = b"blob " + str(len(data)).encode() + b"\0" + data
        if hashlib.sha256(data).hexdigest() != entry["sha256"] or hashlib.sha1(blob).hexdigest() != entry["git_blob"]:
            raise ValueError("Vendored badge icon/CC0 notice differs: " + entry["path"])
    inventory = json.loads((ROOT / "licenses/assets.json").read_text())
    actual = {str(p.relative_to(ROOT)) for p in (ROOT / "assets").rglob("*") if p.is_file()}
    generated = json.loads((ROOT / "assets/badges/manifest.json").read_text())
    if generated.get("schema") != 1:
        raise ValueError("Invalid generated badge inventory")
    actual.discard("assets/badges/manifest.json")
    inventory["assets"].extend(generated["assets"])
    expected = {entry["path"] for entry in inventory["assets"]}
    if len(expected) != len(inventory["assets"]):
        raise ValueError("Duplicate asset inventory paths")
    if actual != expected:
        raise ValueError("Asset inventory must be updated for added or removed assets")
    unresolved = []
    for entry in inventory["assets"]:
        file = ROOT / entry["path"]
        if hashlib.sha256(file.read_bytes()).hexdigest() != entry["sha256"]:
            raise ValueError(f"Asset changed; review its notices: {entry['path']}")
        if entry["runtime"] is not False or not entry["artwork_rights"]:
            raise ValueError("Asset scope and rights status must be explicit")
        if "third_party_notice" in entry and not (ROOT / entry["third_party_notice"]).is_file():
            raise ValueError("Missing third-party badge notice")
        if "font_notice" in entry:
            metadata = next("".join(node.itertext()) for node in ET.parse(file).iter()
                            if node.attrib.get("id") == "font-licenses")
            notice = (ROOT / entry["font_notice"]).read_bytes()
            if notice != metadata.encode() or hashlib.sha256(notice).hexdigest() != entry["font_notice_sha256"]:
                raise ValueError(f"Embedded font notice differs: {entry['path']}")
        if entry.get("license") not in {"MIT", "CC0-1.0", "MIT AND CC0-1.0"}:
            unresolved.append(entry["path"])
    print(f"PASS: source license copies and {len(expected)} asset fingerprints/font notices.")
    print("OPEN RIGHTS REVIEW (excluded from runtime): separate artwork/trademark grants were not supplied for "
          + ", ".join(unresolved))
    print("Runtime transitive notices are verified from actual binaries during each image build and smoke test.")


if __name__ == "__main__":
    main()
