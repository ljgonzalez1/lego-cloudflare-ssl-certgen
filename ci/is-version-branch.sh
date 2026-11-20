#!/usr/bin/env bash
set -euo pipefail

# GitHub branch filters are globs; enforce the exact version grammar here.
# Bash uses POSIX ERE (no non-capturing groups) and accepts case-insensitive suffixes.
shopt -s nocasematch
export LC_ALL=C
numeric='(0|[1-9][0-9]*)'
core="(${numeric}\.${numeric}(\.${numeric}){0,2})"
suffix='((alpha|alfa|beta|audit)([-.]?[1-9][0-9]*)?|(rc|build)[-.]?[1-9][0-9]*)'
branch=${1:-}
[[ "$branch" =~ ^[vV]?${core}([-+.]?${suffix})?$ ]] || exit 1

if [[ ${2:-} == --print-version ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
fi
