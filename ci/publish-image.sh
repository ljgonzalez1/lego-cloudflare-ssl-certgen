#!/usr/bin/env bash
set -euo pipefail

source ci/versions.sh
read_versions

repository=ljgonzalez/lego-cloudflare-certgen
app_minor=${APP_VERSION%.*}
app_major=${APP_VERSION%%.*}
lego_minor=${LEGO_VERSION%.*}
alpine_minor=${ALPINE_VERSION%.*}
tags=(
    "$repository:alpine${ALPINE_VERSION}-lego${LEGO_VERSION}-v${APP_VERSION}"
    "$repository:alpine${alpine_minor}-lego${lego_minor}-v${APP_VERSION}"
    "$repository:alpine${alpine_minor}-lego${lego_minor}-v${app_minor}"
    "$repository:lego${LEGO_VERSION}-v${APP_VERSION}"
    "$repository:lego${lego_minor}-v${APP_VERSION}"
    "$repository:v${APP_VERSION}"
    "$repository:v${app_minor}"
    "$repository:v${app_major}"
    "$repository:latest"
)

if [[ ${1:-} == --print-tags ]]; then
    printf '%s\n' "${tags[@]}"
    exit 0
fi

if (( $# != 0 )); then
    printf 'Usage: bash ci/publish-image.sh [--print-tags]\n' >&2
    exit 2
fi

if [[ ! -s DOCKERHUB.md ]] || (( $(wc -c < DOCKERHUB.md) > 25000 )); then
    printf 'DOCKERHUB.md must contain between 1 and 25000 bytes\n' >&2
    exit 1
fi

: "${DOCKERHUB_USERNAME:?Missing Docker Hub username}"
: "${DOCKERHUB_PUSH_TOKEN:?Missing Docker Hub push token}"

work=$(mktemp -d "${TMPDIR:-/tmp}/certgen-release.XXXXXXXX")
builder="cf-lego-certgen-multiarch-${work##*.}"
builder_created=false
cleanup() {
    if [[ "$builder_created" == true ]]; then
        docker buildx rm "$builder" >/dev/null || true
    fi
    rm -rf -- "$work"
}
trap cleanup EXIT

export DOCKER_CONFIG="$work/docker"
mkdir -m 700 "$DOCKER_CONFIG"

printf '%s' "$DOCKERHUB_PUSH_TOKEN" |
    docker login --username "$DOCKERHUB_USERNAME" --password-stdin
unset DOCKERHUB_PUSH_TOKEN

docker run --privileged --rm tonistiigi/binfmt --install arm,arm64,386
docker run --rm tonistiigi/binfmt
docker buildx ls
docker buildx create --name "$builder" --driver docker-container --use
builder_created=true
docker buildx inspect "$builder" --bootstrap

platforms=linux/arm/v6,linux/arm/v7,linux/arm64,linux/386,linux/amd64
docker buildx build --builder "$builder" --platform "$platforms" --progress=plain \
    "${version_build_args[@]}" --metadata-file "$work/image.json" --tag "${tags[0]}" --push ./build

digest=$(jq -er '."containerimage.digest" | select(test("^sha256:[0-9a-f]{64}$"))' "$work/image.json")
image="$repository@$digest"
docker buildx imagetools inspect --raw "$image" | jq -e '
    [.manifests[] | .platform | select(.os == "linux") |
        if .architecture == "arm" then .architecture + "/" + (.variant // "")
        elif .architecture == "arm64" and ((.variant // "v8") != "v8") then "unexpected-arm64-variant"
        else .architecture end]
    | sort == ["386", "amd64", "arm/v6", "arm/v7", "arm64"]
' >/dev/null

# Attach each alias to the same immutable index. The last registry write is latest.
for tag in "${tags[@]:1}"; do
    docker buildx imagetools create --tag "$tag" "$image"
done

printf 'Published image index %s\n' "$image"
