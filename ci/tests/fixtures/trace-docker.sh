#!/bin/sh
# Offline stand-in for the diagnostic runner's Docker CLI.
printf '%s\n' "$@" > "${TRACE_TEST_ARGS:?}"
printf 'license trace stdout fixture\n'
printf 'license trace stderr fixture\n' >&2
if [ "${TRACE_TEST_STATUS:-0}" -eq 0 ] && [ -n "${TRACE_TEST_EXPORT:-}" ]; then
    for argument do
        case "$argument" in
            type=local,dest=*)
                destination=${argument#type=local,dest=}
                mkdir -p "$destination" || exit 1
                cp -R "$TRACE_TEST_EXPORT/." "$destination/" || exit 1
                ;;
        esac
    done
fi
exit "${TRACE_TEST_STATUS:-0}"
