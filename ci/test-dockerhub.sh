#!/usr/bin/env bash
# Exercise the publishing checks without external requests or real delays.
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
mkdir "$work/bin"
cd -- "$work"
printf '# Example\n\nExpected Markdown.\n' > DOCKERHUB.md
printf '{"schemaVersion":2,"manifests":[]}\n' > expected-index.json
checksum=$(sha256sum expected-index.json)
expected_digest="sha256:${checksum%% *}"
image=ljgonzalez/lego-cloudflare-certgen:v4.2.11

cat > bin/curl <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
url=${!#}
case "$url" in
    https://auth.docker.io/token)
        [[ " $* " == *' scope=repository:ljgonzalez/lego-cloudflare-certgen:pull '* ]]
        printf '{"token":"test-pull-token"}\n'
        ;;
    https://registry-1.docker.io/v2/ljgonzalez/lego-cloudflare-certgen/manifests/v4.2.11)
        [[ " $* " == *' Authorization: Bearer test-pull-token '* ]]
        [[ " $* " == *' application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json '* ]]
        while [[ "${1:-}" != --output ]]; do shift; done
        output=$2
        printf '.\n' >> requests
        case "$SCENARIO:$(wc -l < requests)" in
            index-retry:1) exit 22 ;;
            empty-index:1) cp -- expected-index.json "$output"; exit 22 ;;
            empty-index:*) ;;
            index-retry:2|wrong-index:*) printf '{"schemaVersion":2,"manifests":[{}]}' > "$output" ;;
            *) cp -- expected-index.json "$output" ;;
        esac
        ;;
    https://hub.docker.com/v2/namespaces/ljgonzalez/repositories/lego-cloudflare-certgen)
        printf '.\n' >> requests
        case "$SCENARIO:$(wc -l < requests)" in
            overview-retry:1|wrong-overview:*) printf '{"full_description":"Old Markdown."}\n' ;;
            empty-overview:*) ;;
            *) printf '{"full_description":"# Example\\r\\n\\r\\nExpected Markdown.\\r\\n \\t"}\n' ;;
        esac
        ;;
    *) exit 90 ;;
esac
MOCK
cat > bin/sleep <<'MOCK'
#!/usr/bin/env bash
[[ "$1" == 15 ]]
MOCK
chmod +x bin/*
export PATH="$work/bin:$PATH"

check() {
    local scenario=$1 expected_status=$2 expected_requests=$3 status=0
    shift 3
    : > requests
    SCENARIO="$scenario" bash "$root/ci/check-dockerhub.sh" "$@" > result 2>&1 || status=$?
    if [[ "$status" != "$expected_status" || "$(wc -l < requests)" != "$expected_requests" ]]; then
        printf 'Unexpected verification result for %s (exit %s).\n' "$scenario" "$status" >&2
        cat result >&2
        return 1
    fi
}

check index-retry 0 3 image "$image" "$expected_digest"
grep -Fq "Verified $image at $expected_digest" result
check wrong-index 1 5 image "$image" "$expected_digest"
grep -Fq "expected $expected_digest, got sha256:" result
check empty-index 1 5 image "$image" "$expected_digest"
check invalid-tag 2 0 image "${image%:*}:latest" "$expected_digest"
check wrong-repository 2 0 image v4.2.11 "$expected_digest"
check invalid-digest 2 0 image "$image" wrong-digest
check overview-retry 0 2 description
check wrong-overview 1 5 description
grep -Fq 'description differs from DOCKERHUB.md' result
check empty-overview 1 5 description
grep -Fq 'does not contain a Markdown description' result
printf 'Docker Hub verification tests passed.\n'
