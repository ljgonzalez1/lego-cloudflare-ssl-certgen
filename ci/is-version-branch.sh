#!/usr/bin/env bash
set -euo pipefail

version='[vV]?[0-9]+(\.[0-9]+){1,3}'
suffix='[.-](build|audit|review|beta|alpha|alfa)([.-][0-9]+(\.[0-9]+){0,4})?'
[[ "$1" =~ ^${version}(${suffix})?$ ]]
