#!/usr/bin/env bash
set -euo pipefail

document=${1:?Missing Markdown document}
case "${GITHUB_EVENT_NAME:-}" in
    release) ;;
    push|pull_request)
        if [[ "${TARGET_BRANCH:-}" != master ]] && ! bash ci/is-version-branch.sh "${TARGET_BRANCH:-}"; then
            printf 'Invalid version branch: %s\n' "${TARGET_BRANCH:-}" >&2
            exit 1
        fi
        ;;
    *) printf 'Version checks are only valid for push, pull_request and release.\n' >&2; exit 1 ;;
esac

# README and DOCKERHUB must agree with versions.txt and the CSV, not the Git ref.
python3 -B ci/versions/check_version.py "$document"
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    echo 'eligible=true' >> "$GITHUB_OUTPUT"
fi
