#!/usr/bin/env bash
set -euo pipefail

platform=$1
image=$2

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
sed -e 's/^EMAIL=.*/EMAIL=ci@example.com/' \
    -e 's/^PRODUCTION=.*/PRODUCTION=false/' \
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
[[ "$version" == 4.2.2 ]] || { printf 'Expected image 4.2.2, found %s\n' "$version" >&2; exit 1; }

args=(--rm --pull=never --platform "$platform" --network none)
lego_arch=${platform#linux/}
lego_arch=${lego_arch%%/*}
lego_version=$(docker run "${args[@]}" --entrypoint /usr/local/bin/lego "$image" --version)
[[ "$lego_version" == "lego version 4.34.0 linux/$lego_arch" ]]

args+=(--env-file "$work/.env"
    --mount "type=bind,source=$work/domains.yaml,target=/app/domains.yaml,readonly")
groups=$(docker run "${args[@]}" "$image" --list)
printf '%s\n' "$groups"
[[ "$groups" == 'smoke: 3 domains' ]]

plan=$(docker run "${args[@]}" "$image" --staging --dry-run)
printf '%s\n' "$plan"
grep -Fxq '🧪 STAGING MODE' <<< "$plan"
grep -Fxq 'ℹ️  ACME server: https://acme-staging-v02.api.letsencrypt.org/directory' <<< "$plan"
grep -Fxq 'ℹ️  Domains (3):' <<< "$plan"
for domain in example.com www.example.com '*.example.com'; do
    grep -Fxq "🔹 $domain" <<< "$plan"
done
printf 'PASS: %s\n' "$platform"
