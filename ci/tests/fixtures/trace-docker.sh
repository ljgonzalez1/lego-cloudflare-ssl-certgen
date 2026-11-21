#!/bin/sh
# Offline stand-in for the build trace runner's Docker CLI.
case "$1 $2" in
    "buildx inspect")
        [ -z "${TRACE_TEST_MISSING_BUILDER:-}" ]
        exit
        ;;
    "buildx create")
        printf '%s\n' "$@" > "${TRACE_TEST_CREATE:?}"
        exit 0
        ;;
esac
printf '%s\n' "$@" > "${TRACE_TEST_ARGS:?}"
printf 'trace stdout fixture\n'
printf 'trace stderr fixture\n' >&2
exit "${TRACE_TEST_STATUS:-0}"
