#!/usr/bin/env bash
set -euo pipefail

source ci/versions.sh
read_versions

platform=${1:?Usage: bash ci/run-smoke.sh linux/ARCH}
case "$platform" in
    linux/arm/v6|linux/arm/v7|linux/arm64|linux/386|linux/amd64) ;;
    *) printf 'Unsupported smoke-test platform: %s\n' "$platform" >&2; exit 2 ;;
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
name="certgen-smoke-$id"
docker buildx create --name "$name" --driver docker-container --driver-opt image=moby/buildkit:v0.35.1
builder=$name
docker buildx build --builder "$builder" "${options[@]}" --load ./build

bash ci/tests/smoke.sh "$platform" "$image"
