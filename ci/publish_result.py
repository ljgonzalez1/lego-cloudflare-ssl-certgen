#!/usr/bin/env python3
"""Collect immutable Buildx platform digests and export validated GitHub job outputs."""

import csv
import hashlib
import json
from pathlib import Path
import re
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "ci/versions"))
from release_config import release_config

PLATFORMS = {
    "linux/arm/v6": "armv6", "linux/arm/v7": "armv7", "linux/arm64": "arm64",
    "linux/386": "i386", "linux/amd64": "amd64",
}
REPOSITORY = "ljgonzalez/lego-cloudflare-certgen"


def valid_digest(value):
    return isinstance(value, str) and re.fullmatch(r"sha256:[0-9a-f]{64}", value) is not None


def validate_result(result):
    values, _ = release_config()
    if not isinstance(result, dict) or result.get("image") != f"{REPOSITORY}:{values['APP_TAG']}":
        raise ValueError("The publisher image differs from this checkout's full application version")
    if not valid_digest(result.get("digest")):
        raise ValueError("The publisher index digest must be a SHA-256 string")
    digests = result.get("digests")
    if not isinstance(digests, dict) or set(digests) != set(PLATFORMS.values()) or not all(map(valid_digest, digests.values())):
        raise ValueError("The publisher must return one valid SHA-256 digest for each of the five platforms")
    return result


def collect(metadata_file, index_file):
    metadata = json.loads(Path(metadata_file).read_bytes())
    digest = metadata.get("containerimage.digest")
    if not valid_digest(digest):
        raise ValueError("Buildx metadata does not contain a valid containerimage.digest")
    if metadata.get("containerimage.descriptor", {}).get("digest", digest) != digest:
        raise ValueError("Buildx metadata descriptor and index digests disagree")
    raw = Path(index_file).read_bytes()
    # imagetools --raw can append one CLI newline after the original manifest bytes.
    candidates = (raw, raw[:-1]) if raw.endswith(b"\n") else (raw,)
    if not any("sha256:" + hashlib.sha256(data).hexdigest() == digest for data in candidates):
        raise ValueError("The immutable index bytes differ from the Buildx metadata digest")
    index = json.loads(raw)
    if index.get("schemaVersion") != 2 or index.get("mediaType") not in {
        "application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json",
    }:
        raise ValueError("Buildx must publish a multi-platform index")
    digests = {}
    for manifest in index["manifests"]:
        platform = manifest.get("platform", {})
        if platform.get("os") != "linux":
            continue  # BuildKit attestations are not runnable Linux images.
        architecture = platform.get("architecture", "")
        name = "linux/" + architecture
        if architecture == "arm":
            name += "/" + platform.get("variant", "")
        if architecture == "arm64" and platform.get("variant", "v8") not in (None, "", "v8"):
            raise ValueError("Unexpected ARM64 variant in the build index")
        key = PLATFORMS.get(name)
        if key is None or key in digests or not valid_digest(manifest.get("digest")):
            raise ValueError(f"Missing, duplicate, unexpected or invalid build platform: {name}")
        digests[key] = manifest["digest"]
    values, _ = release_config()
    return validate_result({"image": f"{REPOSITORY}:{values['APP_TAG']}", "digest": digest, "digests": digests})


def main():
    try:
        if len(sys.argv) == 5 and sys.argv[1] == "collect":
            result = collect(sys.argv[2], sys.argv[3])
            Path(sys.argv[4]).write_text(json.dumps(result, indent=2) + "\n")
            print(f"Buildx index digest: {result['digest']}")
            for platform, key in PLATFORMS.items():
                print(f"Buildx {platform}: {result['digests'][key]}")
        elif len(sys.argv) == 3 and sys.argv[1] == "outputs":
            result = validate_result(json.loads(Path(sys.argv[2]).read_bytes()))
            # Repository names containing DOCKERHUB_USERNAME can be suppressed as secrets.
            # Export only hashes; later jobs derive the repository and version locally.
            lines = [f"digest={result['digest']}"]
            lines.extend(f"digest_{key}={result['digests'][key]}" for key in PLATFORMS.values())
            print("\n".join(lines))
        else:
            raise ValueError("Usage: publish_result.py collect METADATA INDEX RESULT | outputs RESULT")
    except (OSError, ValueError, TypeError, KeyError, AttributeError, csv.Error) as error:
        print(f"Publish result error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
