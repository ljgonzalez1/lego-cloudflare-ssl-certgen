#!/bin/sh
# Emit a fatal header in separate writes while a child keeps the pipe open.
sleep 30 &
printf '%s\n' "$!" > "$1"
printf '2026/11/17 06:53:55 Could not com' >&2
sleep 0.1
printf 'plete registration\n\tACME directory unavailable\n' >&2
wait
