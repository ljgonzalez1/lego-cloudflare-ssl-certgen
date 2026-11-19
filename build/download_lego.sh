#!/bin/sh
# Download the official, statically linked lego release for the target platform.
set -eu

case "${TARGETARCH:-}/${TARGETVARIANT:-}" in
    amd64/|amd64/v1) release_arch=linux_amd64 ;;
    386/) release_arch=linux_386 ;;
    arm/v6) release_arch=linux_armv6 ;;
    arm/v7) release_arch=linux_armv7 ;;
    arm64/|arm64/v8) release_arch=linux_arm64 ;;
    *)
        printf 'Unsupported target platform. Use linux/amd64, linux/386, linux/arm/v6, linux/arm/v7 or linux/arm64.\n' >&2
        exit 2
        ;;
esac

version="${LEGO_VERSION:?The lego release version is required}"
release="https://github.com/go-acme/lego/releases/download/${version}"
archive="${LEGO_TARBALL:-lego_${version}_${release_arch}.tar.gz}"
case "$archive" in
    .*|-*|*[!A-Za-z0-9._-]*)
        printf 'LEGO_TARBALL must be a release archive filename.\n' >&2
        exit 2
        ;;
esac
output="${1:?An output directory is required}"
work_dir="$(mktemp -d)"
trap 'rm -rf -- "${work_dir}"' 0

checksum="${LEGO_SHA256:-}"
if [ -z "${checksum}" ]; then
    curl -fsSL --connect-timeout 15 --max-time 180 --retry 3 \
        -o "${work_dir}/checksums" "${release}/lego_${version#v}_checksums.txt"
    checksum="$(awk -v name="${archive}" '$2 == name { print $1; exit }' "${work_dir}/checksums")"
fi
case "${checksum}" in
    ''|*[!0-9a-fA-F]*)
        printf 'Missing or invalid SHA-256 for %s.\n' "${archive}" >&2
        exit 1
        ;;
esac
if [ "${#checksum}" -ne 64 ]; then
    printf 'Invalid SHA-256 length for %s.\n' "${archive}" >&2
    exit 1
fi

curl -fsSL --connect-timeout 15 --max-time 180 --retry 3 \
    -o "${work_dir}/lego.tar.gz" "${LEGO_URL:-${release}/${archive}}"
printf '%s  %s\n' "${checksum}" "${work_dir}/lego.tar.gz" | sha256sum -c -
payload="${work_dir}/payload"
mkdir -- "$payload"
tar -xzf "${work_dir}/lego.tar.gz" -C "$payload" lego LICENSE
for name in lego LICENSE; do
    if [ -L "$payload/$name" ] || [ ! -f "$payload/$name" ] || [ ! -s "$payload/$name" ]; then
        printf 'The lego archive must contain a nonempty regular %s file.\n' "$name" >&2
        exit 1
    fi
    if [ -L "$output/$name" ] || [ -d "$output/$name" ]; then
        printf 'Refusing to replace a link or directory at %s/%s.\n' "$output" "$name" >&2
        exit 1
    fi
done
if [ "$payload/lego" -ef "$payload/LICENSE" ]; then
    printf 'The lego executable and license must be distinct files.\n' >&2
    exit 1
fi
chmod 755 "$payload/lego"
chmod 644 "$payload/LICENSE"
mkdir -p -- "${output}"
mv -f -- "$payload/lego" "$payload/LICENSE" "$output/"
