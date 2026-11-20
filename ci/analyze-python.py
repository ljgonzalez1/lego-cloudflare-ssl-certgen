#!/usr/bin/env python3
"""Compile all first-party Python sources in memory and run the pinned Ruff linter."""

import subprocess
import sys
from source_files import ROOT, files, kind


def main():
    paths = [path for path in files() if kind(path) == "python"]
    for path in paths:
        compile(path.read_bytes(), str(path.relative_to(ROOT)), "exec")
    print(f"Validated syntax in {len(paths)} Python files.", flush=True)
    return subprocess.call(["ruff", "check", "--config", "ci/ruff.toml",
                            *(str(path.relative_to(ROOT)) for path in paths)], cwd=ROOT)


if __name__ == "__main__":
    sys.exit(main())
