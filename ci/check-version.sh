#!/usr/bin/env bash
set -euo pipefail

document=${1:?Missing Markdown document}
if [[ "$GITHUB_EVENT_NAME" != release && "$TARGET_BRANCH" != master ]] &&
    ! bash ci/is-version-branch.sh "$TARGET_BRANCH"; then
    printf 'Branch %s is outside the version branch policy.\n' "$TARGET_BRANCH"
    echo 'eligible=false' >> "$GITHUB_OUTPUT"
    exit 0
fi

python3 -B ci/versions/check_version.py "$document" --ref "$VERSION_REF"
echo 'eligible=true' >> "$GITHUB_OUTPUT"
