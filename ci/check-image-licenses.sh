#!/usr/bin/env bash
# Verify the exported final image, without running target-architecture license tools.
set -euo pipefail
image=${1:?Usage: bash ci/check-image-licenses.sh IMAGE PLATFORM}
platform=${2:?Missing platform}
work=$(mktemp -d)
container=
cleanup() {
    if [[ -n "$container" ]]; then docker rm -fv "$container" >/dev/null; fi
    rm -rf -- "$work"
}
trap cleanup EXIT
container=$(docker create --platform "$platform" "$image")
docker export "$container" > "$work/runtime.tar"
mkdir "$work/root"
python3 - "$work/runtime.tar" "$work/root" <<'PY'
import sys
import tarfile
with tarfile.open(sys.argv[1]) as archive:
    archive.extractall(sys.argv[2], filter="data")
PY
CGO_ENABLED=0 go build -C build -buildvcs=false -mod=vendor -trimpath \
    -ldflags='-s -w' -o "$work/certgen-license-check" ./cmd/certgen-build
"$work/certgen-license-check" verify-licenses "$work/root"
printf 'PASS: final runtime license inventory (%s)\n' "$platform"
