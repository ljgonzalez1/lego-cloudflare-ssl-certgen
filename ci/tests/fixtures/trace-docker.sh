#!/bin/sh
# Offline stand-in for the diagnostic runner's Docker CLI.
printf '%s\n' "$@" > "${TRACE_TEST_ARGS:?}"
printf 'license trace stdout fixture\n'
printf 'license trace stderr fixture\n' >&2
exit "${TRACE_TEST_STATUS:-0}"
