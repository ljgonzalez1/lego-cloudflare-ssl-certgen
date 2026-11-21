#!/usr/bin/env bash
# Trace the complete target image build without publishing or loading it.
set -uo pipefail

platform=${1:-all}
case "$platform" in
    all) platform=linux/arm/v6,linux/arm/v7,linux/arm64,linux/386,linux/amd64 ;;
    linux/arm/v6|linux/arm/v7|linux/arm64|linux/386|linux/amd64) ;;
    *) printf 'Usage: bash ci/trace-build.sh [linux/ARCH|all] [LOG_PATH]\n' >&2; exit 2 ;;
esac
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd) || exit 1
cd -- "$root" || exit 1
log=${2:-$root/trace.log}
builder=${CERTGEN_BUILDER:-cf-lego-certgen-multiarch}

# Reuse the named builder, or create it with the BuildKit pinned for CI.
if ! docker buildx inspect "$builder" >/dev/null 2>&1; then
    docker buildx create --name "$builder" --driver docker-container \
        --driver-opt image=moby/buildkit:v0.35.1 >/dev/null || exit 1
    printf 'Created Buildx builder %s\n' "$builder"
fi

docker buildx build \
    --builder "$builder" \
    --platform "$platform" \
    --target target \
    --no-cache \
    --progress=plain \
    --output=type=cacheonly \
    ./build 2>&1 | tee -- "$log"
statuses=("${PIPESTATUS[@]}")
printf '\nBuildx exit status: %s\nLog: %s\n' "${statuses[0]}" "$log"
if (( statuses[0] != 0 )); then
    exit "${statuses[0]}"
fi
exit "${statuses[1]}"
