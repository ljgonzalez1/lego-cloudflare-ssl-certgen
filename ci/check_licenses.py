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
    inventory = json.loads((ROOT / "licenses/assets.json").read_text())
    actual = {str(p.relative_to(ROOT)) for p in (ROOT / "assets").iterdir() if p.is_file()}
    expected = {entry["path"] for entry in inventory["assets"]}
    if actual != expected:
        raise ValueError("Asset inventory must be updated for added or removed assets")
    unresolved = []
    for entry in inventory["assets"]:
        file = ROOT / entry["path"]
        if hashlib.sha256(file.read_bytes()).hexdigest() != entry["sha256"]:
            raise ValueError(f"Asset changed; review its notices: {entry['path']}")
        if entry["runtime"] is not False or not entry["artwork_rights"]:
            raise ValueError("Asset scope and rights status must be explicit")
        if "font_notice" in entry:
            metadata = next("".join(node.itertext()) for node in ET.parse(file).iter()
                            if node.attrib.get("id") == "font-licenses")
            notice = (ROOT / entry["font_notice"]).read_bytes()
            if notice != metadata.encode() or hashlib.sha256(notice).hexdigest() != entry["font_notice_sha256"]:
                raise ValueError(f"Embedded font notice differs: {entry['path']}")
        if entry.get("license") != "MIT":
            unresolved.append(entry["path"])
    print(f"PASS: source license copies and {len(expected)} asset fingerprints/font notices.")
    print("OPEN RIGHTS REVIEW (excluded from runtime): separate artwork/trademark grants were not supplied for "
          + ", ".join(unresolved))
    print("Runtime transitive notices are verified from actual binaries during each image build and smoke test.")


if __name__ == "__main__":
    main()
