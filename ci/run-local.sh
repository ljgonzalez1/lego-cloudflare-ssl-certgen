#!/usr/bin/env bash
# Run locally, in workflow order, every GitHub Actions check that gates a
# Docker Hub release, then the release build itself without login or push.
set -euo pipefail

usage() {
    printf 'Usage: bash ci/run-local.sh [all|static|versions|offline|go|smoke|release]...\n' >&2
    exit 2
}

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd -- "$root"
export GOTOOLCHAIN=local
sections=("$@")
(( ${#sections[@]} > 0 )) || sections=(all)
for section in "${sections[@]}"; do
    case "$section" in all|static|versions|offline|go|smoke|release) ;; *) usage ;; esac
done

step() { printf '\n==> %s\n' "$*"; }
run() { printf '+ %s\n' "$*"; "$@"; }
missing=()
need() { command -v "$1" >/dev/null || missing+=("$1"); }
wanted() {
    local item
    for item in "${sections[@]}"; do
        [[ "$item" == all || "$item" == "$1" ]] && return 0
    done
    return 1
}

# Check every tool first, so a long run does not stop halfway on a missing one.
need python3
python3 -c 'import yaml' 2>/dev/null || missing+=("python3-yaml")
if wanted static || wanted offline || wanted go || wanted smoke; then
    need go
    if command -v go >/dev/null && [[ $(go env GOVERSION) != go1.27.4 ]]; then
        missing+=("Go 1.27.4 (found $(go env GOVERSION))")
    fi
fi
if wanted static; then
    need shellcheck; need jq; need gawk
    command -v ruff >/dev/null || python3 -c 'import ruff' 2>/dev/null || missing+=("ruff (python3 -m pip install -r ci/requirements-lint.txt)")
fi
if wanted offline; then need jq; need script; fi
if wanted go; then need gcc; fi
if wanted smoke || wanted release; then
    need docker
    command -v docker >/dev/null && ! docker buildx version >/dev/null 2>&1 && missing+=("docker buildx")
fi
if wanted offline && [[ $(id -u) == 0 ]]; then
    missing+=("a non-root user (the regressions reject UID 0, as on GitHub runners)")
fi
if (( ${#missing[@]} > 0 )); then
    printf 'Missing requirements:\n' >&2
    printf '  - %s\n' "${missing[@]}" >&2
    exit 1
fi

if wanted static; then
    # Jobs static_go ... static_formats. Advisory on releases, blocking on PRs to master.
    for language in go python bash posix jq awk sed dockerfile yaml actions csv json modules formats; do
        step "Static analysis: $language"
        run python3 -B ci/analyze.py "$language"
    done
fi

if wanted versions; then
    # Jobs version_readme and version_dockerhub, as for a release event.
    for document in README.md DOCKERHUB.md; do
        step "Version table: $document"
        run env GITHUB_EVENT_NAME=release bash ci/check-version.sh "$document"
    done
fi

if wanted offline; then
    # Job offline.
    step "Offline issuance, menu, error and signal regressions"
    run sh ci/tests/run.sh
    step "Docker Hub verification logic"
    run bash ci/test-dockerhub.sh
    step "Buildx metadata, platform hashes and release tags"
    run python3 -B ci/test_publish.py
    step "Build trace runner"
    run python3 -B ci/test_trace_build.py
    step "CI event routing and job dependencies"
    run python3 -B ci/test_workflows.py
    step "Prerelease tag policy"
    run python3 -B ci/versions/test_tags.py
fi

if wanted go; then
    # Job go_tests.
    flags=(-buildvcs=false -mod=vendor "-gcflags=all=-N -l -d=checkptr=2" -ldflags=-compressdwarf=false)
    step "Go unit tests (shuffled, coverage)"
    (cd build && run env CGO_ENABLED=0 go test "${flags[@]}" -shuffle=on -count=1 -timeout=5m -coverprofile=coverage.out ./...)
    step "Go race detector"
    (cd build && run env CGO_ENABLED=1 go test -race "${flags[@]}" -shuffle=on -count=1 -timeout=5m ./...)
    for target in FuzzParseOptions FuzzLoadDomains FuzzLegoLog; do
        step "Go fuzzing: $target"
        (cd build && run env CGO_ENABLED=0 go test "${flags[@]}" -run='^$' -fuzz="^${target}\$" \
            -fuzztime=15s -parallel=2 -timeout=2m ./internal/certgen)
    done
    step "Real Go binaries, logs, dumps and five debug ELF targets"
    run python3 -B ci/test_go_binary.py
fi

if wanted smoke || wanted release; then
    # GitHub runs ARM64 natively; on an AMD64 machine it needs QEMU like ARM and i386.
    step "Register QEMU for arm, arm64 and 386"
    run docker run --privileged --rm tonistiigi/binfmt:qemu-v10.2.3-68 --install arm,arm64,386
fi

if wanted smoke; then
    # Jobs smoke_armv6, smoke_armv7, smoke_arm64, smoke_i386 and smoke_amd64.
    for platform in linux/arm/v6 linux/arm/v7 linux/arm64 linux/386 linux/amd64; do
        step "Smoke test: $platform"
        run bash ci/run-smoke.sh "$platform"
    done
fi

if wanted release; then
    # Job publish up to the push: same tag checks, builder and five-platform build.
    app_version=$(sed -n 's/^APP_VERSION=//p' ci/versions/versions.txt)
    step "Release build for v$app_version (no login, no push)"
    run env RELEASE_TAG="v$app_version" bash ci/publish-image.sh --no-push
fi

printf '\nAll requested checks passed: %s\n' "${sections[*]}"
