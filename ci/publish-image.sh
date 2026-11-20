#!/usr/bin/env bash
set -euo pipefail

source ci/versions.sh
read_versions

if [[ ${RELEASE_TAG+x} && "${RELEASE_TAG#[vV]}" != "$APP_VERSION" ]]; then
    printf 'Git release tag %s does not match APP_VERSION=%s.\n' "$RELEASE_TAG" "$APP_VERSION" >&2
    exit 2
fi

repository=ljgonzalez/lego-cloudflare-certgen
release_image="$repository:v${APP_VERSION}"
app_number=${APP_VERSION%%-*}
app_suffix=${APP_VERSION#"$app_number"}
app_minor="${app_number%.*}$app_suffix"
app_major="${app_number%%.*}$app_suffix"
lego_minor=${LEGO_VERSION%.*}
alpine_minor=${ALPINE_VERSION%.*}
tags=(
    "$repository:alpine${ALPINE_VERSION}-lego${LEGO_VERSION}-v${APP_VERSION}"
    "$repository:alpine${alpine_minor}-lego${lego_minor}-v${APP_VERSION}"
    "$repository:alpine${alpine_minor}-lego${lego_minor}-v${app_minor}"
    "$repository:lego${LEGO_VERSION}-v${APP_VERSION}"
    "$repository:lego${lego_minor}-v${APP_VERSION}"
    "$release_image"
    "$repository:v${app_minor}"
    "$repository:v${app_major}"
)
prerelease='(^|[._-])(alpha|alfa|beta)[0-9]*($|[._-])'
if [[ ! "${app_suffix,,}" =~ $prerelease ]]; then
    tags+=("$repository:latest")
fi
for tag in "${tags[@]}"; do
    if [[ ! "${tag#*:}" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ ]]; then
        printf 'Invalid Docker tag: %s\n' "$tag" >&2
        exit 2
    fi
done

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
docker buildx imagetools inspect --raw "$image" | jq -e '[.manifests[] | .platform]' |
    jq -e -f ci/image-platforms.jq >/dev/null

# Attach each alias to the same immutable index. When present, latest is last.
for tag in "${tags[@]:1}"; do
    docker buildx imagetools create --tag "$tag" "$image"
done

if [[ -n ${PUBLISH_RESULT_FILE:-} ]]; then
    jq -n --arg image "$release_image" --arg digest "$digest" \
        '{image: $image, digest: $digest}' > "$PUBLISH_RESULT_FILE"
fi

printf 'Published image index %s\n' "$image"
