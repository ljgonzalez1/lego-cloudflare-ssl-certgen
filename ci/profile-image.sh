#!/usr/bin/env bash
# Measure the final image with its real lego --version workload, without ACME orders.
set -euo pipefail
image=${1:?Usage: bash ci/profile-image.sh IMAGE PLATFORM}
platform=${2:?Missing platform}
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
case "$platform" in
    linux/arm/v6) architecture=arm; variant=6 ;;
    linux/arm/v7) architecture=arm; variant=7 ;;
    linux/arm64) architecture=arm64; variant= ;;
    linux/386) architecture=386; variant= ;;
    linux/amd64) architecture=amd64; variant= ;;
    *) printf 'Unsupported platform: %s\n' "$platform" >&2; exit 2 ;;
esac
CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" GOARM="$variant" \
    go build -buildvcs=false -trimpath -ldflags='-s -w' -o "$work/probe" ci/probe/main.go
printf 'sample: [example.com, www.example.com, "*.example.com"]\n' > "$work/domains.yaml"
chmod 644 "$work/domains.yaml"
mkdir "$work/metrics"
docker run --rm --network none --platform "$platform" \
    --env EMAIL=ci@example.com --env ACCEPT_LEGO_TOS=true --env UID=1000 --env GID=1000 \
    --mount "type=bind,source=$work/probe,target=/probe,readonly" \
    --mount "type=bind,source=$work/domains.yaml,target=/app/domains.yaml,readonly" \
    --mount "type=bind,source=$work/metrics,target=/metrics" \
    --entrypoint /probe "$image" /metrics/runtime.json /usr/local/bin/certgen --test
image_size=$(docker image inspect --format '{{.Size}}' "$image")
python3 - "$work/metrics/runtime.json" "$image_size" "$platform" <<'PY'
import json
import os
from pathlib import Path
import sys
data=json.loads(Path(sys.argv[1]).read_text())
assert data["exit_code"]==0 and data["wait4_max_rss_kib"]>0
data.update(image_uncompressed_layer_bytes=int(sys.argv[2]),platform=sys.argv[3],workload="offline --test with real lego --version")
text=json.dumps(data,indent=2)
print(text)
if os.environ.get("GITHUB_STEP_SUMMARY"):
    with open(os.environ["GITHUB_STEP_SUMMARY"],"a",encoding="utf-8") as summary:
        summary.write("\n### Final image measurements: "+sys.argv[3]+"\n\n```json\n"+text+"\n```\n")
PY
