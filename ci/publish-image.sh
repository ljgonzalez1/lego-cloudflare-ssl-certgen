#!/usr/bin/env bash
set -euo pipefail

source ci/versions.sh
read_versions

if [[ ${RELEASE_TAG+x} && "${RELEASE_TAG#[vV]}" != "$APP_VERSION" ]]; then
    printf 'Git release tag %s does not match APP_VERSION=%s.\n' "$RELEASE_TAG" "$APP_VERSION" >&2
    exit 2
fi

repository=ljgonzalez/lego-cloudflare-certgen
tags=()
for tag in "${release_tags[@]}"; do tags+=("$repository:$tag"); done

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

docker run --privileged --rm tonistiigi/binfmt:qemu-v10.2.3-68 --install arm,arm64,386
docker run --rm tonistiigi/binfmt:qemu-v10.2.3-68
docker buildx ls
docker buildx create --name "$builder" --driver docker-container --driver-opt image=moby/buildkit:v0.35.1 --use
builder_created=true
docker buildx inspect "$builder" --bootstrap

platforms=linux/arm/v6,linux/arm/v7,linux/arm64,linux/386,linux/amd64
printf 'Building release %s for %s\n' "$APP_TAG" "$platforms"
docker buildx build --builder "$builder" --platform "$platforms" --progress=plain \
    "${version_build_args[@]}" --metadata-file "$work/build-metadata.json" --tag "${tags[0]}" --push ./build

digest=$(jq -er '."containerimage.digest" | select(test("^sha256:[0-9a-f]{64}$"))' "$work/build-metadata.json")
image="$repository@$digest"
printf 'Reading platform hashes from immutable build index %s\n' "$image"
docker buildx imagetools inspect --raw "$image" > "$work/build-index.json"
python3 -B ci/publish_result.py collect "$work/build-metadata.json" "$work/build-index.json" "$work/result.json"

# Attach each alias to the same immutable index. When eligible, the last registry write is latest.
for tag in "${tags[@]:1}"; do
    printf 'Publishing alias %s at %s\n' "$tag" "$digest"
    docker buildx imagetools create --tag "$tag" "$image"
done

if [[ -n ${PUBLISH_RESULT_FILE:-} ]]; then
    cp -- "$work/result.json" "$PUBLISH_RESULT_FILE"
fi

printf 'Published image index %s\n' "$image"
