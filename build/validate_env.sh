#!/usr/bin/env bash
# ==============================================================================
# validate_env.sh  --  Environment variable validation module
# ==============================================================================
# Sourced by entrypoint.sh for UID/GID and run_lego.sh for issuance settings.
# ==============================================================================

if [[ -t 1 ]]; then
    _C_RST=$'\033[0m' _C_BOLD=$'\033[1m'
    _C_RED=$'\033[31m' _C_GRN=$'\033[32m' _C_YLW=$'\033[33m'
    _C_BLU=$'\033[34m' _C_MAG=$'\033[35m' _C_CYN=$'\033[36m'
else
    _C_RST='' _C_BOLD=''
    _C_RED='' _C_GRN='' _C_YLW='' _C_BLU='' _C_MAG='' _C_CYN=''
fi

log_info()    { printf '%sℹ️  %s%s\n' "${_C_CYN}" "$*" "${_C_RST}"; }
log_step()    { printf '%s🔹 %s%s\n' "${_C_BLU}" "$*" "${_C_RST}"; }
log_ok()      { printf '%s✅ %s%s\n' "${_C_GRN}" "$*" "${_C_RST}"; }
log_warn()    { printf '%s⚠️  %s%s\n' "${_C_YLW}" "$*" "${_C_RST}"; }
log_err()     { printf '%s❌ %s%s\n' "${_C_RED}" "$*" "${_C_RST}" >&2; }
log_section() { printf '\n%s%s%s\n' "${_C_BOLD}${_C_MAG}" "$*" "${_C_RST}"; }

validate_identity() {
    # Bash reserves UID as a read-only variable. Read the supplied env value.
    local raw_uid raw_gid
    raw_uid="${CERT_UID:-$(printenv UID 2>/dev/null || true)}"
    raw_gid="${CERT_GID:-${GID:-}}"
    raw_uid="${raw_uid:-1000}"
    raw_gid="${raw_gid:-1000}"

    if ! [[ "${raw_uid}" =~ ^[0-9]{1,10}$ ]] || (( 10#${raw_uid} < 1 || 10#${raw_uid} > 4294967294 )); then
        log_err "UID must be an integer from 1 to 4294967294."
        return 2
    fi
    if ! [[ "${raw_gid}" =~ ^[0-9]{1,10}$ ]] || (( 10#${raw_gid} < 1 || 10#${raw_gid} > 4294967294 )); then
        log_err "GID must be an integer from 1 to 4294967294."
        return 2
    fi
    CERT_UID="$((10#${raw_uid}))"
    CERT_GID="$((10#${raw_gid}))"
    export CERT_UID CERT_GID
}

validate_env() {
    local production="${PRODUCTION:-false}"
    local accepted="${ACCEPT_LEGO_TOS:-false}"
    local resolvers="${DNS_RESOLVERS:-1.1.1.1:53,8.8.8.8:53,1.0.0.1:53}"
    local resolver port
    local resolver_pattern='^([a-zA-Z0-9_.-]+|\[[0-9a-fA-F:.]+\]):[0-9]{1,5}$'
    local -a errors=()

    VALIDATED_TZ="${TZ:-Etc/UTC}"
    if [[ "${VALIDATED_TZ}" == /* || "${VALIDATED_TZ}" == *..* ||
          ! -f "/usr/share/zoneinfo/${VALIDATED_TZ}" ]]; then
        errors+=("TZ: expected an IANA timezone such as Etc/UTC or America/Santiago.")
    fi

    VALIDATED_EMAIL="${EMAIL:-}"
    if [[ -n "${VALIDATED_EMAIL}" || "${CERTGEN_ACTION}" == issue ]]; then
        if ! [[ "${VALIDATED_EMAIL}" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]]; then
            errors+=("EMAIL: enter a valid Let's Encrypt contact address.")
        fi
    fi

    VALIDATED_PRODUCTION="${production,,}"
    case "${VALIDATED_PRODUCTION}" in
        true) ACME_SERVER=https://acme-v02.api.letsencrypt.org/directory ;;
        false) ACME_SERVER=https://acme-staging-v02.api.letsencrypt.org/directory ;;
        *) errors+=("PRODUCTION: expected true or false.") ;;
    esac

    if [[ "${CERTGEN_ACTION}" == issue && "${accepted,,}" != true ]]; then
        errors+=("ACCEPT_LEGO_TOS: set to true after reviewing https://letsencrypt.org/repository/")
    fi

    VALIDATED_PROPAGATION="${PROPAGATION_SECONDS:-60}"
    if ! [[ "${VALIDATED_PROPAGATION}" =~ ^[0-9]{1,10}$ ]] ||
        (( 10#${VALIDATED_PROPAGATION} > 2147483647 )); then
        errors+=("PROPAGATION_SECONDS: expected a non-negative integer in seconds.")
    else
        VALIDATED_PROPAGATION="$((10#${VALIDATED_PROPAGATION}))"
    fi

    resolvers="${resolvers// /}"
    IFS=',' read -ra VALIDATED_DNS_RESOLVERS <<< "${resolvers}"
    if [[ "${resolvers}" == *, || "${resolvers}" =~ [[:space:]] ]]; then
        errors+=("DNS_RESOLVERS: expected comma-separated host:port entries.")
    else
        for resolver in "${VALIDATED_DNS_RESOLVERS[@]}"; do
            port="${resolver##*:}"
            if ! [[ "${resolver}" =~ ${resolver_pattern} ]] ||
                (( 10#${port} < 1 || 10#${port} > 65535 )); then
                errors+=("DNS_RESOLVERS: invalid resolver '${resolver}'.")
            fi
        done
    fi

    if (( ${#errors[@]} )); then
        log_err "Environment validation failed."
        printf '  %s\n' "${errors[@]}" >&2
        return 2
    fi
}

configure_cloudflare() {
    local secret=/run/secrets/cloudflare_api_token
    local token="${CLOUDFLARE_API_KEY:-${CF_DNS_API_TOKEN:-${CLOUDFLARE_API_TOKEN:-}}}"
    local use_file=false

    if [[ -z "${token}" && -r "${secret}" ]]; then
        token="$(< "${secret}")"
        use_file=true
    fi
    if ! [[ "${token}" =~ ^[a-zA-Z0-9_-]+$ ]]; then
        log_err "Provide a valid Cloudflare DNS API token through the Compose secret or CLOUDFLARE_API_KEY."
        return 2
    fi

    # The wrapper's legacy CLOUDFLARE_API_KEY setting is an API token.
    # Clear lego's legacy Global API Key settings before passing that token.
    unset CLOUDFLARE_API_KEY CLOUDFLARE_API_KEY_FILE CF_API_KEY CF_API_KEY_FILE CLOUDFLARE_API_TOKEN
    unset CF_DNS_API_TOKEN CF_DNS_API_TOKEN_FILE CLOUDFLARE_DNS_API_TOKEN CLOUDFLARE_DNS_API_TOKEN_FILE
    if [[ "${use_file}" == true ]]; then
        export CF_DNS_API_TOKEN_FILE="${secret}"
    else
        export CF_DNS_API_TOKEN="${token}"
    fi
    export CLOUDFLARE_PROPAGATION_TIMEOUT="${VALIDATED_PROPAGATION}"
}
