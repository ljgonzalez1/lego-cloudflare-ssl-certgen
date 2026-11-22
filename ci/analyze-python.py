#!/usr/bin/env python3
"""Compile all first-party Python sources in memory and run the pinned Ruff linter."""

import importlib.util
import shutil
import subprocess
import sys
from source_files import ROOT, files, kind


def ruff_command():
    """Ruff from PATH, or the module installed for this Python interpreter."""
    if shutil.which("ruff"):
        return ["ruff"]
    if importlib.util.find_spec("ruff"):
        return [sys.executable, "-m", "ruff"]
    raise SystemExit("Ruff is not installed. Run: python3 -m pip install -r ci/requirements-lint.txt")


def main():
    paths = [path for path in files() if kind(path) == "python"]
    for path in paths:
        compile(path.read_bytes(), str(path.relative_to(ROOT)), "exec")
    print(f"Validated syntax in {len(paths)} Python files.", flush=True)
    return subprocess.call([*ruff_command(), "check", "--config", "ci/ruff.toml",
                            *(str(path.relative_to(ROOT)) for path in paths)], cwd=ROOT)


if __name__ == "__main__":
    sys.exit(main())
