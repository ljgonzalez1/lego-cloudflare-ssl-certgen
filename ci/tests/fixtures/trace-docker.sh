#!/bin/sh
# Offline stand-in for the build trace runner's Docker CLI.
printf '%s\n' "$@" > "${TRACE_TEST_ARGS:?}"
printf 'trace stdout fixture\n'
printf 'trace stderr fixture\n' >&2
exit "${TRACE_TEST_STATUS:-0}"
