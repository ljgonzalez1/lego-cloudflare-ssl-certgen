#!/usr/bin/env bash
# Check every first-party shell file, including nested regression fixtures.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
mapfile -d '' -t scripts < <(python3 -B ci/source_files.py shell)
if (( ${#scripts[@]} == 0 )); then
    printf 'No shell scripts found\n' >&2
    exit 1
fi
for script in "${scripts[@]}"; do
    case "$(head -n 1 "$script")" in
        '#!/bin/sh'|'#!/usr/bin/env sh') sh -n "$script" ;;
        *) bash -n "$script" ;;
    esac
done
shellcheck --external-sources --severity=style "${scripts[@]}"
