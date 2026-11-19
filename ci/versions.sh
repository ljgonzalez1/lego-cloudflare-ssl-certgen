# Source this file with Bash, then call read_versions from the repository root.
read_versions() {
    local configuration key value
    configuration=$(python3 -B versions/release_config.py) || return 1
    release_tags=()
    while IFS=$'\t' read -r key value; do
        case "$key" in
            APP_VERSION|LEGO_VERSION|ALPINE_VERSION|APP_TAG) printf -v "$key" '%s' "$value" ;;
            TAG) release_tags+=("$value") ;;
            *) printf 'Unexpected release configuration key: %s\n' "$key" >&2; return 1 ;;
        esac
    done <<< "$configuration"
    version_build_args=(
        --build-arg "APP_VERSION=$APP_VERSION"
        --build-arg "LEGO_VERSION=v$LEGO_VERSION"
        --build-arg "ALPINE_VERSION=$ALPINE_VERSION"
    )
}
