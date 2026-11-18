#!/usr/bin/env bash
# Run the offline HTTP regression suite from any working directory.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
exec python3 -B "$root/ci/test_dockerhub.py"
