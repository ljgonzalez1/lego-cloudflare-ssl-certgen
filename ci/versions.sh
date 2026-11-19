# Source this file with Bash, then call read_versions from the repository root.
read_versions() {
    local key line value pattern
    local format='^(APP_VERSION|LEGO_VERSION|ALPINE_VERSION)=(.+)$'
    local numeric='(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*)){2}'
    local suffix='(-[A-Za-z0-9_][A-Za-z0-9_.-]*)?'
    local -A seen=()

    if [[ ! -r ci/versions/versions.txt ]]; then
        printf 'Cannot read ci/versions/versions.txt\n' >&2
        return 1
    fi

    while IFS= read -r line || [[ -n "$line" ]]; do
        line=${line%$'\r'}
        [[ -z "$line" || "$line" == \#* ]] && continue
        if [[ ! "$line" =~ $format ]]; then
            printf 'Invalid entry in ci/versions/versions.txt\n' >&2
            return 1
        fi

        key=${BASH_REMATCH[1]}
        value=${BASH_REMATCH[2]}
        pattern="^${numeric}$"
        if [[ "$key" == APP_VERSION ]]; then
            pattern="^${numeric}${suffix}$"
        fi
        if [[ ! "$value" =~ $pattern ]]; then
            printf 'Invalid version for %s in ci/versions/versions.txt\n' "$key" >&2
            return 1
        fi
        if [[ ${seen[$key]+found} ]]; then
            printf 'Duplicate version key: %s\n' "$key" >&2
            return 1
        fi

        printf -v "$key" '%s' "$value"
        seen[$key]=1
    done < ci/versions/versions.txt

    for key in APP_VERSION LEGO_VERSION ALPINE_VERSION; do
        if [[ ! ${seen[$key]+found} ]]; then
            printf 'Missing %s in ci/versions/versions.txt\n' "$key" >&2
            return 1
        fi
    done

    version_build_args=(
        --build-arg "APP_VERSION=$APP_VERSION"
        --build-arg "LEGO_VERSION=v$LEGO_VERSION"
        --build-arg "ALPINE_VERSION=$ALPINE_VERSION"
    )
}
