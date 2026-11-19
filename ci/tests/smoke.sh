#!/usr/bin/env bash
set -euo pipefail

source ci/versions.sh
read_versions

platform=$1
image=$2

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
sed -e 's/^EMAIL=.*/EMAIL=ci@example.com/' \
    -e 's/^ACCEPT_LEGO_TOS=.*/ACCEPT_LEGO_TOS=true/' \
    -e 's/^CLOUDFLARE_API_TOKEN=.*/CLOUDFLARE_API_TOKEN=/' \
    example.env > "$work/.env"
grep -Fxq 'ACCEPT_LEGO_TOS=true' "$work/.env"

cat > "$work/domains.yaml" <<'YAML'
smoke:
  - example.com
  - www.example.com
  - '*.example.com'
YAML
chmod 644 "$work/domains.yaml"

version=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "$image")
if [[ "$version" != "$APP_VERSION" ]]; then
    printf 'Expected image %s, found %s\n' "$APP_VERSION" "$version" >&2
    exit 1
fi

alpine_version=$(docker image inspect --format '{{index .Config.Labels "com.certgen.alpine.version"}}' "$image")
if [[ "$alpine_version" != "$ALPINE_VERSION" ]]; then
    printf 'Expected Alpine %s, found %s\n' "$ALPINE_VERSION" "$alpine_version" >&2
    exit 1
fi

args=(--rm --pull=never --platform "$platform" --network none)
lego_arch=${platform#linux/}
lego_arch=${lego_arch%%/*}

args+=(--env-file "$work/.env"
    --mount "type=bind,source=$work/domains.yaml,target=/app/domains.yaml,readonly")
plan=$(docker run "${args[@]}" "$image" --test)
printf '%s\n' "$plan"
grep -Fxq '🧪 TEST MODE (offline)' <<< "$plan"
grep -Fxq "ℹ️  lego version $LEGO_VERSION linux/$lego_arch" <<< "$plan"
grep -Fxq '✅ Offline test passed.' <<< "$plan"
grep -Fxq '🧪 STAGING MODE' <<< "$plan"
grep -Fxq 'ℹ️  ACME server: https://acme-staging-v02.api.letsencrypt.org/directory' <<< "$plan"
grep -Fxq 'ℹ️  Domains (3):' <<< "$plan"
for domain in example.com www.example.com '*.example.com'; do
    grep -Fxq "🔹 $domain" <<< "$plan"
done
printf 'PASS: %s\n' "$platform"
