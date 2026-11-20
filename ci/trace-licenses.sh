#!/usr/bin/env bash
# Re-run the real license stage, preserve its status through tee, and save a log.
set -uo pipefail

platform=${1:-linux/amd64}
case "$platform" in
    all) platform=linux/arm/v6,linux/arm/v7,linux/arm64,linux/386,linux/amd64 ;;
    linux/arm/v6|linux/arm/v7|linux/arm64|linux/386|linux/amd64) ;;
    *) printf 'Usage: bash ci/trace-licenses.sh [linux/ARCH|all] [LOG_PATH]\n' >&2; exit 2 ;;
esac
name=${1:-linux/amd64}
log=${2:-${TMPDIR:-/tmp}/certgen-beta4-license-${name//\//-}.log}
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd) || exit 1
cd -- "$root" || exit 1

docker buildx build \
    --builder "${CERTGEN_BUILDER:-cf-lego-certgen-multiarch}" \
    --platform "$platform" \
    --target licensed \
    --no-cache-filter licensed \
    --progress=plain \
    --output=type=cacheonly \
    ./build 2>&1 | tee -- "$log"
statuses=("${PIPESTATUS[@]}")
printf '\nBuildx exit status: %s\nLog: %s\n' "${statuses[0]}" "$log"
if (( statuses[0] != 0 )); then
    exit "${statuses[0]}"
fi
exit "${statuses[1]}"
