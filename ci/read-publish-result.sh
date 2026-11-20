#!/usr/bin/env bash
# Export only the exact image and digest produced for this checkout's version.
set -euo pipefail
source ci/versions.sh
read_versions
result=${1:?Missing publisher result file}
jq -ers --arg image "ljgonzalez/lego-cloudflare-certgen:v$APP_VERSION" '
    if length != 1 or (.[0] | type) != "object" then
        error("The publisher must return one JSON result object")
    elif .[0].image != $image then
        error("The publisher image differs from the configured full application version")
    elif (.[0].digest | type) != "string" then
        error("The publisher digest must be a SHA-256 string")
    elif (.[0].digest | test("^sha256:[0-9a-f]{64}$") | not) then
        error("The publisher digest must be a SHA-256 string")
    else .[0] | "image=\(.image)\ndigest=\(.digest)" end
' "$result"
