#!/usr/bin/env bash
set -euo pipefail

version='[vV]?([0-9]+(\.[0-9]+){1,3})'
suffix='[.-](build|audit|review|beta|alpha|alfa)([.-][0-9]+(\.[0-9]+){0,4})?'
root=${1%%/*}
[[ "$root" =~ ^${version}(${suffix})?$ ]] || exit 1

if [[ ${2:-} == --print-version ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
fi
