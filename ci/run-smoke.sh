#!/usr/bin/env bash
set -euo pipefail

source ci/versions.sh
read_versions

platform=${1:?Usage: bash ci/run-smoke.sh linux/ARCH}
case "$platform" in
    linux/arm/v6|linux/arm/v7|linux/arm64|linux/386|linux/amd64) ;;
    *) printf 'Unsupported smoke-test platform: %s\n' "$platform" >&2; exit 2 ;;
esac

case "$(uname -m)" in
    x86_64) native_platform=linux/amd64 ;;
    aarch64|arm64) native_platform=linux/arm64 ;;
    *) printf 'Unsupported smoke-test agent architecture\n' >&2; exit 2 ;;
esac

scratch=$(mktemp -d "${TMPDIR:-/tmp}/certgen-smoke.XXXXXXXX")
id=${scratch##*.}
image="certgen-smoke:$id"
builder=
cleanup() {
    if [[ -n "$builder" ]]; then
        docker buildx rm "$builder" >/dev/null || true
    fi
    docker image rm "$image" >/dev/null 2>&1 || true
    rmdir "$scratch"
}
trap cleanup EXIT

options=(--platform "$platform" --progress=plain --tag "$image" "${version_build_args[@]}")
if [[ "$platform" == "$native_platform" ]]; then
    docker build "${options[@]}" ./build
else
    name="certgen-smoke-$id"
    docker buildx create --name "$name" --driver docker-container
    builder=$name
    docker buildx build --builder "$builder" "${options[@]}" --load ./build
fi

bash tests/smoke.sh "$platform" "$image"
