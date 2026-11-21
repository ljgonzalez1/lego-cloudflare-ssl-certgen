#!/usr/bin/env bash
# Authenticate sources once and retain only the notice texts in the project.
set -uo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd) || exit 1
cd -- "$root" || exit 1
log=${1:-$root/trace-license-bundle.log}
if (( $# > 1 )); then
    printf 'Usage: bash ci/update-license-bundle.sh [LOG_PATH]\n' >&2
    exit 2
fi
work=$(mktemp -d "${TMPDIR:-/tmp}/certgen-license-export.XXXXXXXX") || exit 1
trap 'rm -rf -- "$work"' EXIT

docker buildx build \
    --builder "${CERTGEN_BUILDER:-cf-lego-certgen-multiarch}" \
    --platform linux/amd64 \
    --target license-bundle-export \
    --build-arg REFRESH_LICENSE_BUNDLE=1 \
    --progress=plain \
    --output "type=local,dest=$work/export" \
    ./build 2>&1 | tee -- "$log"
statuses=("${PIPESTATUS[@]}")
printf '\nBuildx exit status: %s\nLog: %s\n' "${statuses[0]}" "$log"
if (( statuses[0] != 0 )); then
    exit "${statuses[0]}"
fi
if (( statuses[1] != 0 )); then
    exit "${statuses[1]}"
fi
python3 -B ci/license_bundle.py install "$work/export" "$root/build/licenses/third-party" 2>&1 | tee --append -- "$log"
statuses=("${PIPESTATUS[@]}")
if (( statuses[0] != 0 )); then
    exit "${statuses[0]}"
fi
exit "${statuses[1]}"
