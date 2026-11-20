#!/bin/sh
# Deliberately wait in a separate process group and ignore graceful termination.
trap '' TERM
sleep 30 &
printf '%s\n' "$!" > "$1"
wait
