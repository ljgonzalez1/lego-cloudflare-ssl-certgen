#!/usr/bin/env python3
"""Compile Python sources in memory, then check errors with the pinned Ruff linter."""

from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent


def main() -> int:
    paths = sorted((ROOT / "ci").rglob("*.py"))
    for path in paths:
        compile(path.read_bytes(), str(path.relative_to(ROOT)), "exec")
    print(f"Validated syntax in {len(paths)} Python files.", flush=True)
    return subprocess.call(["ruff", "check", "--config", "ci/ruff.toml", "ci"], cwd=ROOT)


if __name__ == "__main__":
    sys.exit(main())
