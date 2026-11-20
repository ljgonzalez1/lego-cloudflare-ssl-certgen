#!/usr/bin/env bash
set -euo pipefail

source ci/versions.sh
read_versions

platform=$1
image=$2
bash ci/check-image-licenses.sh "$image" "$platform"

work=$(mktemp -d)
container=
trap 'if [[ -n "$container" ]]; then docker rm -fv "$container" >/dev/null; fi; rm -rf -- "$work"' EXIT
smoke_uid=$(id -u)
smoke_gid=$(id -g)
[[ "$smoke_uid" != 0 ]] || smoke_uid=1000
[[ "$smoke_gid" != 0 ]] || smoke_gid=1000
sed -e 's/^EMAIL=.*/EMAIL=ci@example.com/' \
    -e 's/^ACCEPT_LEGO_TOS=.*/ACCEPT_LEGO_TOS=true/' \
    -e 's/^CLOUDFLARE_API_TOKEN=.*/CLOUDFLARE_API_TOKEN=/' \
    -e "s/^UID=.*/UID=$smoke_uid/" \
    -e "s/^GID=.*/GID=$smoke_gid/" \
    template.env > "$work/.env"
grep -Fxq 'ACCEPT_LEGO_TOS=true' "$work/.env"

cat > "$work/domains.yaml" <<'YAML'
smoke:
  - example.com
  - www.example.com
  - '*.example.com'
YAML
chmod 644 "$work/domains.yaml"
mkdir "$work/debug"

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
container=$(docker create --platform "$platform" "$image")
docker cp "$container:/usr/share/certgen/debugger.json" "$work/debugger.json"
python3 - "$work/debugger.json" "$platform" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
assert data["go_traces"] is True
assert isinstance(data["delve"], bool)
assert isinstance(data["reason"], str) and data["reason"]
arch = {"linux/arm/v6":"armv6", "linux/arm/v7":"armv7", "linux/386":"i386"}.get(sys.argv[2], sys.argv[2].removeprefix("linux/"))
assert data["architecture"] == arch
if sys.argv[2] in {"linux/arm/v6", "linux/arm/v7"}:
    assert data["delve"] is False
PY
if python3 -c 'import json,sys; sys.exit(not json.load(open(sys.argv[1]))["delve"])' "$work/debugger.json"; then
    docker cp "$container:/usr/local/bin/dlv" "$work/dlv"
    args+=(--cap-add SYS_PTRACE --security-opt seccomp=unconfined)
else
    if docker cp "$container:/usr/local/bin/dlv" "$work/dlv" >/dev/null 2>&1; then
        printf 'An unusable debugger was included in the final filesystem\n' >&2
        exit 1
    fi
fi
docker rm -v "$container" >/dev/null
container=
lego_arch=${platform#linux/}
lego_arch=${lego_arch%%/*}

args+=(--env-file "$work/.env"
    --mount "type=bind,source=$work/domains.yaml,target=/app/domains.yaml,readonly"
    --mount "type=bind,source=$work/debug,target=/ssl-certs/debug")
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
case "$platform" in
    linux/arm/v6) log_arch=armv6 ;;
    linux/arm/v7) log_arch=armv7 ;;
    linux/386) log_arch=i386 ;;
    *) log_arch=${platform#linux/} ;;
esac
shopt -s nullglob
logs=("$work/debug/v${APP_VERSION}."*."$log_arch.log")
(( ${#logs[@]} == 1 ))
grep -Fq 'Offline test passed.' "${logs[0]}"
grep -Fq 'target final snapshot' "${logs[0]}"
grep -Fq 'goroutine ' "${logs[0]}"
dumps=("$work/debug/dump."*)
(( ${#dumps[@]} == 0 ))
[[ $(stat -c '%a' "${logs[0]}") == 600 ]]
bash ci/profile-image.sh "$image" "$platform"
printf 'PASS: %s\n' "$platform"
