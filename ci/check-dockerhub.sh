#!/usr/bin/env bash
set -euo pipefail

repository=ljgonzalez/lego-cloudflare-certgen
curl_options=(--fail --silent --show-error --connect-timeout 10 --max-time 30)

retry() {
    local attempt
    for attempt in 1 2 3 4 5; do
        printf 'Docker Hub verification attempt %s/5\n' "$attempt"
        if "$@"; then
            return 0
        fi
        if (( attempt < 5 )); then
            sleep 15
        fi
    done
    printf 'Docker Hub verification failed after 5 attempts.\n' >&2
    return 1
}

check_image() {
    local checksum actual
    if [[ -z "$token" ]]; then
        if ! token=$(curl "${curl_options[@]}" --get \
            --data-urlencode 'service=registry.docker.io' \
            --data-urlencode "scope=repository:$repository:pull" \
            https://auth.docker.io/token | jq -er '(.token // .access_token) | strings | select(length > 0)') || [[ -z "$token" ]]; then
            printf 'Could not get a pull token for %s.\n' "$repository" >&2
            return 1
        fi
    fi

    # Accept both index formats and hash the original response bytes.
    : > "$work/index.json" || return 1
    if ! curl "${curl_options[@]}" \
        --header "Authorization: Bearer $token" \
        --header 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json' \
        --output "$work/index.json" "https://registry-1.docker.io/v2/$repository/manifests/$tag"; then
        printf 'Could not fetch the index for %s.\n' "$image" >&2
        token=
        return 1
    fi
    checksum=$(sha256sum "$work/index.json") || return 1
    actual="sha256:${checksum%% *}"
    if [[ "$actual" != "$digest" ]]; then
        printf '%s: expected %s, got %s.\n' "$image" "$digest" "$actual" >&2
        return 1
    fi
    printf 'Verified %s at %s\n' "$image" "$digest"
}

check_description() {
    if ! curl "${curl_options[@]}" \
        "https://hub.docker.com/v2/namespaces/${repository%/*}/repositories/${repository#*/}" |
        jq -es --rawfile expected DOCKERHUB.md '
            def normalize: gsub("\r\n"; "\n") | sub("[ \t\n]+$"; "");
            if length != 1 or (.[0].full_description | type) != "string" then
                error("Docker Hub response does not contain a Markdown description")
            elif (.[0].full_description | normalize) != ($expected | normalize) then
                error("Docker Hub description differs from DOCKERHUB.md")
            else true end
        ' >/dev/null; then
        return 1
    fi
    printf 'Docker Hub description matches DOCKERHUB.md\n'
}

case "${1:-}" in
    image)
        image=${2:?Missing image tag}
        digest=${3:?Missing expected digest}
        tag=${image#"$repository:"}
        if [[ "$image" != "$repository":* || ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            printf 'Expected an exact version tag for %s.\n' "$repository" >&2
            exit 2
        fi
        if [[ ! "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
            printf 'Invalid expected digest: %s\n' "$digest" >&2
            exit 2
        fi
        work=$(mktemp -d)
        trap 'rm -rf -- "$work"' EXIT
        token=
        retry check_image
        ;;
    description)
        if [[ ! -r DOCKERHUB.md || ! -s DOCKERHUB.md ]]; then
            printf 'DOCKERHUB.md must be a nonempty, readable file.\n' >&2
            exit 2
        fi
        retry check_description
        ;;
    *)
        printf 'Usage: bash ci/check-dockerhub.sh image IMAGE:TAG DIGEST | description\n' >&2
        exit 2
        ;;
esac
