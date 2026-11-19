#!/usr/bin/env bash
# Validate the entire result before emitting any GitHub outputs.
set -euo pipefail
exec python3 -B ci/publish_result.py outputs "${1:?Missing publisher result file}"
