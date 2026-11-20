#!/usr/bin/env python3
"""Discover all first-party source and configuration files, including nested fixtures."""

from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parent.parent
EXCLUDED = {".git", "vendor", "node_modules", "__pycache__", ".ruff_cache", ".venv", ".pytest_cache"}
SUFFIXES = {".go": "go", ".py": "python", ".sh": "shell", ".bash": "shell",
            ".yml": "yaml", ".yaml": "yaml", ".json": "json", ".jq": "jq", ".awk": "awk", ".sed": "sed",
            ".toml": "toml", ".md": "markdown", ".csv": "csv", ".svg": "svg",
            ".png": "png", ".txt": "text", ".env": "env"}


def files(root=ROOT):
    return sorted(path for path in root.rglob("*") if path.is_file()
                  and path.relative_to(root).as_posix() not in {
                      "trace.log"}
                  and not any(part in EXCLUDED for part in path.relative_to(root).parts))


def kind(path):
    if path.name == "Dockerfile" or path.name.startswith("Dockerfile."):
        return "dockerfile"
    if path.name in {"go.mod", "go.sum", ".gitignore", ".dockerignore", ".gitattributes"}:
        return "text"
    if path.name.startswith(("LICENSE", "COPYING", "NOTICE", "AUTHORS", "COPYRIGHT", "PATENTS")):
        return "text"
    if path.suffix in SUFFIXES:
        return SUFFIXES[path.suffix]
    header = path.read_bytes().split(b"\n", 1)[0]
    if header.startswith(b"#!"):
        if b"python" in header:
            return "python"
        if b"sh" in header:
            return "shell"
    raise ValueError("No static validator registered for " + str(path))


def main():
    requested = sys.argv[1]
    if requested == "go-directories":
        directories = sorted({str(path.parent.relative_to(ROOT)) for path in files()
                              if kind(path) == "go" and path.relative_to(ROOT).parts[0] != "build"})
        for directory in directories:
            sys.stdout.buffer.write(directory.encode() + b"\0")
        return
    for path in files():
        if kind(path) == requested:
            sys.stdout.buffer.write(str(path.relative_to(ROOT)).encode() + b"\0")


if __name__ == "__main__":
    main()
