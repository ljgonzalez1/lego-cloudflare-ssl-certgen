#!/usr/bin/env bash
# ==============================================================================
# validate_domains.sh  --  Domain file cleaning and validation module
# ==============================================================================
# SOURCE this file (it only defines functions); do NOT execute it directly.
# Sourced by validate_env.sh, which provides _vfail() / the _ERRORS array used
# to accumulate user-facing errors.  A minimal fallback _vfail() is defined
# below so this module can be unit-tested in isolation.
#
# Public entry point:
#   resolve_domains
#     Reads /domains.txt, cleans and validates it, and on success exports:
#       VALIDATED_DOMAINS  -- comma-separated, cleaned, de-duplicated list
#       DOMAINS_SOURCE     -- human-readable description of where it came from
#     On any problem it records errors via _vfail() and returns 1.
#
# Domain source:
#   - A readable regular file must be mounted at /domains.txt.
#   - The old DOMAINS environment variable is intentionally not used.
#
# Cleaning rules applied to every entry:
#   - entries may be separated by newlines and/or commas
#   - surrounding single/double quotes are removed
#   - leading/trailing whitespace is removed (internal whitespace is NOT, so it
#     correctly invalidates values such as "exam ple.com")
#   - trailing dots are removed ("example.com." -> "example.com")
#   - blank entries are skipped
#   - duplicates are removed, preserving first-seen order
#
# Validation rules (RFC 1123 host names):
#   - at least two labels (one dot)
#   - each label 1-63 chars, alphanumeric with internal hyphens only
#   - total length <= 253
#   - at most one wildcard, and only as the left-most label ("*.example.com")
# ==============================================================================

# Fallback accumulator/logger for isolated unit testing -------------------------
if ! declare -f _vfail >/dev/null 2>&1; then
    _ERRORS=()
    _vfail() { _ERRORS+=("$*"); }
fi

# RFC 1123 host name (>= 2 labels), applied after stripping a leading wildcard.
_DOMAIN_LABEL_RE='^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$'

# ------------------------------------------------------------------------------
# _domains_trim <string>  ->  echoes string without leading/trailing whitespace
# ------------------------------------------------------------------------------
_domains_trim() {
    local s="$1"
    s="${s#"${s%%[![:space:]]*}"}"
    s="${s%"${s##*[![:space:]]}"}"
    printf '%s' "${s}"
}

# ------------------------------------------------------------------------------
# domains_clean_list <raw>  ->  prints cleaned, de-duplicated entries (one/line)
# Performs cleaning only; no validation.  Output preserves first-seen order.
# ------------------------------------------------------------------------------
domains_clean_list() {
    local raw="$1"
    local line token k
    local -a out=()

    # Normalise: drop a UTF-8 BOM, strip CR (CRLF files), commas act as newlines
    raw="${raw#$'\xef\xbb\xbf'}"
    raw="${raw//$'\r'/}"
    raw="${raw//,/$'\n'}"

    while IFS= read -r line || [[ -n "${line}" ]]; do
        token="$(_domains_trim "${line}")"

        # Strip a surrounding run of single/double quotes, then re-trim
        token="${token#"${token%%[!\"\']*}"}"
        token="${token%"${token##*[!\"\']}"}"
        token="$(_domains_trim "${token}")"

        # Strip trailing dots
        while [[ "${token}" == *. ]]; do
            token="${token%.}"
        done
        token="$(_domains_trim "${token}")"

        [[ -z "${token}" ]] && continue

        # De-duplicate (exact match, keep first occurrence)
        local dup=0
        for k in "${out[@]}"; do
            if [[ "${k}" == "${token}" ]]; then
                dup=1
                break
            fi
        done
        [[ "${dup}" -eq 1 ]] && continue

        out+=("${token}")
    done <<< "${raw}"

    [[ "${#out[@]}" -gt 0 ]] && printf '%s\n' "${out[@]}"
    return 0
}

# ------------------------------------------------------------------------------
# domains_validate_entry <domain>  ->  returns 0 if valid, 1 otherwise
# Assumes the entry is already cleaned.
# ------------------------------------------------------------------------------
domains_validate_entry() {
    local d="$1"
    local base="${d}"

    # Reasonable upper bound ("*." prefix + 253-char host name)
    [[ "${#d}" -gt 255 ]] && return 1

    # A single left-most wildcard is allowed; strip it before the host check
    if [[ "${d}" == '*.'* ]]; then
        base="${d#'*.'}"
    fi

    # No wildcard may remain anywhere else (rejects e.g. *.a.*.example.com)
    [[ "${base}" == *'*'* ]] && return 1

    [[ -z "${base}" ]] && return 1
    [[ "${base}" =~ ${_DOMAIN_LABEL_RE} ]] || return 1

    return 0
}

# ------------------------------------------------------------------------------
# _domains_file_accessible <path>  ->  returns 0 if the path is a readable
# regular file, otherwise records a _vfail and returns 1.
#
# Run this in the PARENT shell (not inside $(...)), so that any _vfail it
# records lands in the shared _ERRORS array rather than a discarded subshell.
# ------------------------------------------------------------------------------
_domains_file_accessible() {
    local path="$1"

    if [[ ! -f "${path}" ]]; then
        _vfail "DOMAINS file: '${path}' exists but is not a regular file (is it a directory?)"
        return 1
    fi
    if [[ ! -r "${path}" ]]; then
        _vfail "DOMAINS file: '${path}' is not readable (check file permissions and the volume mount)"
        return 1
    fi
    return 0
}

# ------------------------------------------------------------------------------
# resolve_domains  ->  sets VALIDATED_DOMAINS / DOMAINS_SOURCE or records errors
# ------------------------------------------------------------------------------
resolve_domains() {
    local domains_file="/domains.txt"
    local raw source_desc="file ${domains_file}"

    if [[ ! -e "${domains_file}" ]]; then
        _vfail "DOMAINS file: required file '${domains_file}' was not found"
        _vfail '    Mount it with: --volume "$(pwd)/domains.txt:/domains.txt:ro"'
        return 1
    fi

    _domains_file_accessible "${domains_file}" || return 1
    raw="$(cat -- "${domains_file}")"

    local cleaned
    cleaned="$(domains_clean_list "${raw}")"

    if [[ -z "${cleaned}" ]]; then
        _vfail "DOMAINS file: no usable domain entries found in ${source_desc}"
        return 1
    fi

    local d invalid=0
    local -a valid=()
    while IFS= read -r d; do
        [[ -z "${d}" ]] && continue
        if domains_validate_entry "${d}"; then
            valid+=("${d}")
        else
            _vfail "DOMAINS file: invalid domain '${d}' (from ${source_desc})"
            invalid=1
        fi
    done <<< "${cleaned}"

    if [[ "${invalid}" -eq 1 ]]; then
        return 1
    fi
    if [[ "${#valid[@]}" -eq 0 ]]; then
        _vfail "DOMAINS file: no valid domain entries found in ${source_desc}"
        return 1
    fi

    local joined
    printf -v joined '%s,' "${valid[@]}"
    VALIDATED_DOMAINS="${joined%,}"
    DOMAINS_SOURCE="${source_desc}"
    export VALIDATED_DOMAINS DOMAINS_SOURCE
    return 0
}
