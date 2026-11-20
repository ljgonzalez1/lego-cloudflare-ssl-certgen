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
    -e "s/^UID=.*/UID=$smoke_uid/" -e "s/^GID=.*/GID=$smoke_gid/" \
    template.env > "$work/.env"
cp build/smoke.domains.yaml "$work/domains.yaml"
chmod 644 "$work/domains.yaml"
mkdir "$work/output"
version=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "$image")
[[ "$version" == "$APP_VERSION" ]]
alpine_version=$(docker image inspect --format '{{index .Config.Labels "com.certgen.alpine.version"}}' "$image")
[[ "$alpine_version" == "$ALPINE_VERSION" ]]

# Inspect actual runtime bytes, not Dockerfile text or a debugger manifest.
container=$(docker create --platform "$platform" "$image")
docker export "$container" > "$work/runtime.tar"
python3 - "$work/runtime.tar" <<'PY'
import sys, tarfile, zipfile, io
with tarfile.open(sys.argv[1]) as archive:
    files = {member.name.removeprefix("./"): member for member in archive if member.isfile()}
    expected = {"usr/local/bin/certgen", "usr/local/bin/lego", "usr/share/zoneinfo.zip",
                "etc/ssl/certs/ca-certificates.crt", "etc/passwd", "etc/group"}
    assert expected <= files.keys(), expected - files.keys()
    engine_files = {"etc/hosts", "etc/hostname", "etc/resolv.conf", ".dockerenv"}
    for name in files:
        assert name in expected or name in engine_files or name.startswith("usr/share/licenses/"), "unexpected runtime file: " + name
        if name not in engine_files:
            assert files[name].size > 0, name
    zoneinfo = archive.extractfile(files["usr/share/zoneinfo.zip"]).read()
    with zipfile.ZipFile(io.BytesIO(zoneinfo)) as zones:
        for name in ("America/Santiago", "Etc/UTC", "Europe/London", "Asia/Tokyo"):
            assert zones.read(name).startswith(b"TZif"), name
    print("Runtime files:", len(files), "uncompressed file bytes:", sum(member.size for member in files.values()))
PY
docker rm -v "$container" >/dev/null
container=
args=(--rm --pull=never --platform "$platform" --network none --env-file "$work/.env"
    --mount "type=bind,source=$work/domains.yaml,target=/app/domains.yaml,readonly"
    --mount "type=bind,source=$work/output,target=/ssl-certs")
lego_arch=${platform#linux/}
lego_arch=${lego_arch%%/*}
plan=$(docker run "${args[@]}" --env TZ=America/Santiago "$image" --test)
printf '%s\n' "$plan"
grep -Fxq '🧪 TEST MODE (offline)' <<< "$plan"
grep -Fxq "ℹ️  lego version $LEGO_VERSION linux/$lego_arch" <<< "$plan"
grep -Fxq '✅ Offline test passed.' <<< "$plan"
grep -Fxq '🧪 STAGING MODE' <<< "$plan"
grep -Fxq 'ℹ️  Domains (3):' <<< "$plan"
for domain in example.com www.example.com '*.example.com'; do
    grep -Fxq "🔹 $domain" <<< "$plan"
done
[[ -z $(find "$work/output" -mindepth 1 -print -quit) ]]
app_version=$(docker run "${args[@]}" "$image" --version)
[[ "$app_version" == "certgen version $APP_VERSION linux/$lego_arch" ]]
if docker run "${args[@]}" "$image" --test --production > "$work/invalid" 2>&1; then
    printf 'Conflicting CLI flags were accepted\n' >&2
    exit 1
else
    status=$?
    [[ "$status" == 2 ]]
fi
if grep -Eq 'debug trace|goroutine |/src/' "$work/invalid"; then
    printf 'A diagnostic trace leaked into production output\n' >&2
    exit 1
fi
[[ -z $(find "$work/output" -mindepth 1 -print -quit) ]]
bash ci/profile-image.sh "$image" "$platform"
printf 'PASS: %s\n' "$platform"
