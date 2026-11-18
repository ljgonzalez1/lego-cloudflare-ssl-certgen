#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
    image)
        image=${2:?Missing image tag}
        digest=${3:?Missing expected digest}
        if [[ ! "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
            printf 'Invalid expected digest: %s\n' "$digest" >&2
            exit 2
        fi
        sleep 10
        actual=$(docker buildx imagetools inspect --format '{{.Manifest.Digest}}' "$image")
        if [[ "$actual" != "$digest" ]]; then
            printf '%s: expected %s, got %s\n' "$image" "$digest" "$actual" >&2
            exit 1
        fi
        printf 'Verified %s at %s\n' "$image" "$digest"
        ;;
    description)
        sleep 10
        curl --fail --silent --show-error --max-time 30 \
            https://hub.docker.com/v2/namespaces/ljgonzalez/repositories/lego-cloudflare-certgen |
            jq -e --rawfile expected DOCKERHUB.md '
                def normalize: gsub("\r\n"; "\n") | sub("[ \t\n]+$"; "");
                if (.full_description | type) != "string" then
                    error("Docker Hub response does not contain a Markdown description")
                elif (.full_description | normalize) != ($expected | normalize) then
                    error("Docker Hub description differs from DOCKERHUB.md")
                else true end
            ' >/dev/null
        printf 'Docker Hub description matches DOCKERHUB.md\n'
        ;;
    *)
        printf 'Usage: bash ci/check-dockerhub.sh image IMAGE:TAG DIGEST | description\n' >&2
        exit 2
        ;;
esac
