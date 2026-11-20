#!/usr/bin/env bash
# Format all first-party Go files and vet module/standalone programs for five CPUs.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
mapfile -d '' -t sources < <(python3 -B ci/source_files.py go)
(( ${#sources[@]} > 0 ))
unformatted=$(gofmt -l "${sources[@]}")
if [[ -n "$unformatted" ]]; then
    printf 'Run gofmt on these files:\n%s\n' "$unformatted" >&2
    exit 1
fi
export GOTOOLCHAIN=local GOWORK=off CGO_ENABLED=0 GOOS=linux
for target in armv6 armv7 arm64 386 amd64; do
    (
        unset GOARM
        case "$target" in
            armv6) export GOARCH=arm GOARM=6 ;;
            armv7) export GOARCH=arm GOARM=7 ;;
            *) export GOARCH="$target" ;;
        esac
        printf 'Go vet: linux/%s\n' "$target"
        go vet -C build -buildvcs=false -mod=vendor -tags=netgo,osusergo,certgen_test ./...
        mapfile -d '' -t directories < <(python3 -B ci/source_files.py go-directories)
        for directory in "${directories[@]}"; do
            [[ -z "$directory" ]] || go vet -buildvcs=false "$directory"/*.go
        done
    )
done
