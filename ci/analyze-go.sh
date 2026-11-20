#!/usr/bin/env bash
# Analyze first-party Go code for every shipped Linux architecture without running it.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../build"
export GOTOOLCHAIN=local GOWORK=off CGO_ENABLED=0

directories=(cmd internal)
[[ ! -d spinner ]] || directories+=(spinner)
unformatted=$(gofmt -l "${directories[@]}" ../ci/probe)
if [[ -n "$unformatted" ]]; then
    printf 'Run gofmt on these files:\n%s\n' "$unformatted" >&2
    exit 1
fi

for target in armv6 armv7 arm64 386 amd64; do
    (
        export GOOS=linux
        unset GOARM
        case "$target" in
            armv6) export GOARCH=arm GOARM=6 ;;
            armv7) export GOARCH=arm GOARM=7 ;;
            *) export GOARCH="$target" ;;
        esac
        printf 'Go vet: linux/%s\n' "$target"
        go vet -buildvcs=false -mod=vendor ./...
        go vet -buildvcs=false ../ci/probe/main.go
    )
done
