#!/usr/bin/env bash
# Verify the exact published tag through Docker APIs, never the rendered tags page.
set -euo pipefail

repository=ljgonzalez/lego-cloudflare-certgen
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
curl_options=(--silent --show-error --location --max-redirs 3 --proto '=https' --proto-redir '=https'
    --connect-timeout 10 --max-time 30 --header 'Cache-Control: no-cache')
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
token=
http_status=

retry() {
    local attempt
    for attempt in 1 2 3 4 5; do
        printf 'Docker Hub verification attempt %s/5\n' "$attempt"
        if "$@"; then
            return 0
        fi
        if (( attempt < 5 )); then
            printf 'Retrying the API request; the propagation wait is a separate workflow job.\n'
        fi
    done
    printf 'Docker Hub verification failed after 5 attempts. See the request diagnostics above.\n' >&2
    return 1
}

request() {
    local label=$1 output=$2 url=$3 status=0
    shift 3
    # Truncate every response before curl so retries cannot reuse a previous success.
    : > "$output" || return 1
    : > "$work/headers" || return 1
    http_status=$(curl "${curl_options[@]}" --dump-header "$work/headers" \
        --output "$output" --write-out '%{http_code}' "$@" "$url") || status=$?
    if (( status != 0 )); then
        case "$status" in
            5|6) printf '%s: DNS resolution failed (curl %s).\n' "$label" "$status" >&2 ;;
            28) printf '%s: request timed out (curl 28).\n' "$label" >&2 ;;
            35|60) printf '%s: TLS connection or certificate verification failed (curl %s).\n' "$label" "$status" >&2 ;;
            *) printf '%s: request failed (curl %s, HTTP %s).\n' "$label" "$status" "${http_status:-unknown}" >&2 ;;
        esac
        return 1
    fi
    if [[ "$http_status" != 200 ]]; then
        case "$http_status" in
            401|403) printf '%s: HTTP %s; access was denied.\n' "$label" "$http_status" >&2 ;;
            404) printf '%s: HTTP 404; the exact tag or repository is not visible at this endpoint.\n' "$label" >&2 ;;
            429) printf '%s: HTTP 429; Docker request limit reached.\n' "$label" >&2 ;;
            5??) printf '%s: HTTP %s; Docker service error.\n' "$label" "$http_status" >&2 ;;
            *) printf '%s: unexpected HTTP %s.\n' "$label" "${http_status:-unknown}" >&2 ;;
        esac
        return 1
    fi
    if [[ ! -s "$output" ]]; then
        printf '%s: HTTP 200 contained an empty response.\n' "$label" >&2
        return 1
    fi
}

get_pull_token() {
    if [[ -n "$token" ]]; then
        return 0
    fi
    local credentials=()
    if [[ -n ${DOCKERHUB_USERNAME:-} && -n ${DOCKERHUB_PUSH_TOKEN:-} ]]; then
        credentials=(--user "$DOCKERHUB_USERNAME:$DOCKERHUB_PUSH_TOKEN")
    fi
    if ! request 'Registry authentication' "$work/auth.json" https://auth.docker.io/token \
        --get --data-urlencode 'service=registry.docker.io' \
        --data-urlencode "scope=repository:$repository:pull" "${credentials[@]}"; then
        return 1
    fi
    if ! token=$(jq -ers '
        if length == 1 then .[0] | (.token // .access_token) else null end
        | strings | select(length > 0 and (test("[\\x00-\\x20\\x7f]") | not))
    ' "$work/auth.json"); then
        printf 'Registry authentication did not return a valid JSON pull token.\n' >&2
        token=
        return 1
    fi
}

check_registry_image() {
    local checksum actual header_digest
    get_pull_token || return 1
    if ! request "Registry tag $tag" "$work/index.json" \
        "https://registry-1.docker.io/v2/$repository/manifests/$tag" \
        --header "Authorization: Bearer $token" \
        --header 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json'; then
        if [[ "$http_status" == 401 || "$http_status" == 403 ]]; then token=; fi
        return 1
    fi
    if ! jq -es '
        length == 1 and (.[0] | type == "object" and .schemaVersion == 2 and
            (.mediaType == "application/vnd.oci.image.index.v1+json" or
             .mediaType == "application/vnd.docker.distribution.manifest.list.v2+json") and
            (.manifests | type == "array"))
    ' "$work/index.json" >/dev/null; then
        printf '%s: the registry did not return a JSON multi-platform index.\n' "$image" >&2
        return 1
    fi
    # Hash unmodified response bytes. JSON formatting and newlines are significant.
    checksum=$(sha256sum "$work/index.json") || return 2
    actual="sha256:${checksum%% *}"
    header_digest=$(awk 'tolower($1) == "docker-content-digest:" {gsub("\r", "", $2); value=$2} END {print value}' "$work/headers") || return 2
    if [[ -n "$header_digest" && "$header_digest" != "$actual" ]]; then
        printf '%s: registry digest header %s differs from response digest %s.\n' "$image" "$header_digest" "$actual" >&2
        return 2
    fi
    if [[ "$actual" != "$digest" ]]; then
        printf '%s: expected %s, got %s.\n' "$image" "$digest" "$actual" >&2
        return 2
    fi
    if ! jq -e '[.manifests[] | .platform]' "$work/index.json" |
        jq -e -f "$script_dir/image-platforms.jq" >/dev/null; then
        printf '%s: the index must contain exactly ARMv6, ARMv7, ARM64, i386 and AMD64.\n' "$image" >&2
        return 2
    fi
    printf 'Verified %s at %s (Docker Registry; all five platforms).\n' "$image" "$digest"
}

check_hub_image() {
    # A second API route covers registry routing failures without accepting another tag.
    local url
    for url in \
        "https://hub.docker.com/v2/namespaces/${repository%/*}/repositories/${repository#*/}/tags/$tag" \
        "https://hub.docker.com/v2/repositories/$repository/tags/$tag/"; do
        request "Docker Hub tag $tag" "$work/tag.json" "$url" || continue
        if ! jq -es --arg tag "$tag" --arg digest "$digest" '
            length == 1 and (.[0] | type == "object" and .name == $tag and .digest == $digest and
                (.images | type == "array"))
        ' "$work/tag.json" >/dev/null; then
            printf '%s: Docker Hub returned an invalid tag response or a different tag/digest.\n' "$image" >&2
            continue
        fi
        if ! jq -e '.images' "$work/tag.json" |
            jq -e -f "$script_dir/image-platforms.jq" >/dev/null; then
            printf '%s: Docker Hub has not indexed all five expected platforms.\n' "$image" >&2
            continue
        fi
        printf 'Verified %s at %s (Docker Hub tag API; all five platforms).\n' "$image" "$digest"
        return 0
    done
    return 1
}

check_image() {
    local status=0
    check_registry_image || status=$?
    case "$status" in
        0) return 0 ;;
        2) return 1 ;; # A known digest/platform mismatch must never be hidden by a fallback.
    esac
    printf 'Trying the exact Docker Hub tag API after a registry read failure.\n' >&2
    check_hub_image
}

check_description() {
    local url
    for url in \
        "https://hub.docker.com/v2/namespaces/${repository%/*}/repositories/${repository#*/}" \
        "https://hub.docker.com/v2/repositories/$repository/"; do
        request 'Docker Hub overview' "$work/description.json" "$url" || continue
        if jq -es --rawfile expected DOCKERHUB.md '
            def normalize: gsub("\r\n"; "\n") | sub("[ \t\n]+$"; "");
            if length != 1 or (.[0].full_description | type) != "string" then
                error("Docker Hub response does not contain a Markdown description")
            elif (.[0].full_description | normalize) != ($expected | normalize) then
                error("Docker Hub description differs from DOCKERHUB.md")
            else true end
        ' "$work/description.json" >/dev/null; then
            printf 'Docker Hub description matches DOCKERHUB.md\n'
            return 0
        fi
    done
    return 1
}

case "${1:-}" in
    image)
        if (( $# != 3 )); then
            printf 'Usage: bash ci/check-dockerhub.sh image IMAGE:TAG DIGEST\n' >&2
            exit 2
        fi
        image=$2 digest=$3
        image=${image#docker.io/}
        tag=${image#"$repository:"}
        if [[ "$image" != "$repository":* || ${#tag} -gt 128 ||
              ! "$tag" =~ ^[vV]?[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9_][A-Za-z0-9_.-]*)?$ ]]; then
            printf 'Expected an exact version tag for %s.\n' "$repository" >&2
            exit 2
        fi
        if [[ ! "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
            printf 'Invalid expected SHA-256 digest.\n' >&2
            exit 2
        fi
        retry check_image
        ;;
    description)
        if (( $# != 1 )) || [[ ! -r DOCKERHUB.md || ! -s DOCKERHUB.md ]]; then
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
