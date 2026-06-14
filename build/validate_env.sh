#!/usr/bin/env bash
# ==============================================================================
# validate_env.sh  --  Environment variable validation module
# ==============================================================================
# SOURCE this file from entrypoint.sh; do NOT execute it directly.
#
# Requires log_err() to be defined by the sourcing script.  A minimal fallback
# is provided below in case the file is tested in isolation.
#
# Reads from environment:
#   TZ, EMAIL, PRODUCTION, DOMAINS, CLOUDFLARE_API_KEY,
#   PROPAGATION_SECONDS, DNS_RESOLVERS, ACCEPT_LEGO_TOS, UID, GID
#
# Cloudflare token resolution order:
#   1. CLOUDFLARE_API_KEY  environment variable  (docker run --env)
#   2. /run/secrets/cloudflare_api_token  Docker secret  (docker compose)
#
# Domain source resolution (handled by validate_domains.sh):
#   - mounted file /domains.txt  (takes precedence), OR
#   - DOMAINS environment variable
#
# Sets on success (all exported):
#   VALIDATED_TZ          -- verified IANA timezone string
#   VALIDATED_EMAIL       -- trimmed e-mail address
#   VALIDATED_PRODUCTION  -- "true" or "false" (normalised to lowercase)
#   ACME_SERVER           -- Let's Encrypt directory URL derived from PRODUCTION
#   VALIDATED_DOMAINS     -- cleaned, validated domain string (comma-separated)
#   DOMAINS_SOURCE        -- human-readable description of the domain source
#   VALIDATED_CF_KEY      -- Cloudflare API token (from env var or secret file)
#   VALIDATED_PROPAGATION -- propagation timeout in seconds (string)
#   VALIDATED_DNS_RESOLVERS -- comma-separated host:port resolver list
#   CERT_UID              -- integer UID for certificate file ownership
#   CERT_GID              -- integer GID for certificate file ownership
#
# On validation failure:
#   All errors are collected and printed at once, then the function returns 1.
#   Because the sourcing script uses set -e, a return 1 exits the process.
# ==============================================================================

# Fallback logger used when sourced outside entrypoint.sh (e.g. unit-testing)
if ! declare -f log_err >/dev/null 2>&1; then
    log_err() { printf 'ERROR: %s\n' "$*" >&2; }
fi

# Internal error accumulator
_ERRORS=()
_vfail() { _ERRORS+=("$*"); }

# Domain cleaning/validation helpers (define resolve_domains, etc.)
# shellcheck source=validate_domains.sh
. /usr/local/bin/validate_domains.sh

# ------------------------------------------------------------------------------
# TZ -- IANA timezone
# ------------------------------------------------------------------------------
_v_tz="${TZ:-Etc/UTC}"
if ! TZ="${_v_tz}" date >/dev/null 2>&1; then
    _vfail "TZ: invalid or unrecognised timezone '${_v_tz}'"
    _vfail "    Valid examples: Etc/UTC  America/Santiago  Europe/London"
else
    VALIDATED_TZ="${_v_tz}"
fi

# ------------------------------------------------------------------------------
# EMAIL -- Let's Encrypt account contact address
# ------------------------------------------------------------------------------
_v_email="${EMAIL:-}"
if [[ -z "${_v_email}" ]]; then
    _vfail "EMAIL: required but not set"
elif ! [[ "${_v_email}" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]]; then
    _vfail "EMAIL: invalid e-mail address structure ('${_v_email}')"
else
    VALIDATED_EMAIL="${_v_email}"
fi

# ------------------------------------------------------------------------------
# PRODUCTION -- staging vs real certificate issuance
# Empty value is treated as the default (false).
# ------------------------------------------------------------------------------
_v_prod_raw="${PRODUCTION:-false}"
_v_prod="${_v_prod_raw,,}"   # bash lowercase expansion
if [[ "${_v_prod}" != "true" && "${_v_prod}" != "false" ]]; then
    _vfail "PRODUCTION: must be 'true' or 'false', got '${_v_prod_raw}'"
else
    VALIDATED_PRODUCTION="${_v_prod}"
fi

# Derive ACME server URL from PRODUCTION
if [[ "${VALIDATED_PRODUCTION:-false}" == "true" ]]; then
    ACME_SERVER="https://acme-v02.api.letsencrypt.org/directory"
else
    ACME_SERVER="https://acme-staging-v02.api.letsencrypt.org/directory"
fi

# ------------------------------------------------------------------------------
# ACCEPT_LEGO_TOS -- explicit consent to Let's Encrypt Terms of Service
# The --accept-tos flag in lego accepts the Let's Encrypt Subscriber Agreement.
# ------------------------------------------------------------------------------
_v_tos_raw="${ACCEPT_LEGO_TOS:-false}"
_v_tos="${_v_tos_raw,,}"
if [[ "${_v_tos}" != "true" ]]; then
    _vfail "ACCEPT_LEGO_TOS: must be set to 'true' to accept the Let's Encrypt Terms of Service"
    _vfail "    See: https://letsencrypt.org/repository/"
fi

# ------------------------------------------------------------------------------
# CLOUDFLARE_API_KEY -- Cloudflare DNS API token
#
# Resolution order:
#   1. CLOUDFLARE_API_KEY environment variable  (docker run --env)
#   2. /run/secrets/cloudflare_api_token file   (docker compose secrets)
#
# The token is validated for basic structural integrity.
# Error messages are intentionally vague (no structural hints).
# ------------------------------------------------------------------------------
_v_cf_key="${CLOUDFLARE_API_KEY:-}"

# Fallback: try Docker secret mount (populated by docker compose)
if [[ -z "${_v_cf_key}" ]]; then
    _cf_secret_path="/run/secrets/cloudflare_api_token"
    if [[ -r "${_cf_secret_path}" ]]; then
        # Strip all whitespace (handles trailing newlines from secret file)
        _v_cf_key="$(tr -d '[:space:]' < "${_cf_secret_path}")"
    fi
fi

if [[ -z "${_v_cf_key}" ]]; then
    _vfail "CLOUDFLARE_API_KEY: token not found"
    _vfail "    Provide it via:"
    _vfail "      docker run  ->  --env CLOUDFLARE_API_KEY=\${CLOUDFLARE_API_KEY}"
    _vfail "      docker compose ->  cloudflare_api_token secret (see compose.yml)"
else
    _v_klen="${#_v_cf_key}"
    if [[ "${_v_klen}" -lt 30 || "${_v_klen}" -gt 50 ]] \
    || [[ "${_v_cf_key}" =~ [[:space:]] ]]; then
        _vfail "CLOUDFLARE_API_KEY: invalid token structure"
    else
        VALIDATED_CF_KEY="${_v_cf_key}"
    fi
fi

# ------------------------------------------------------------------------------
# DOMAINS -- domain list for the certificate
#
# The actual source selection (mounted /domains.txt file vs DOMAINS env var),
# cleaning, de-duplication, trailing-dot/quote/whitespace stripping and
# per-domain validation lives in validate_domains.sh::resolve_domains, which
# sets VALIDATED_DOMAINS and DOMAINS_SOURCE on success or records errors via
# _vfail.
#
# '|| true' keeps a failing resolve_domains from tripping the sourcing script's
# 'set -e' before every other validation error has been collected; the _ERRORS
# array is what ultimately triggers the failure exit below.
# ------------------------------------------------------------------------------
resolve_domains || true

# ------------------------------------------------------------------------------
# PROPAGATION_SECONDS -- DNS TXT record propagation wait
# Empty value defaults to 60.
# ------------------------------------------------------------------------------
_v_prop="${PROPAGATION_SECONDS:-60}"
if ! [[ "${_v_prop}" =~ ^[0-9]+$ ]]; then
    _vfail "PROPAGATION_SECONDS: must be a non-negative integer, got '${_v_prop}'"
else
    VALIDATED_PROPAGATION="${_v_prop}"
fi

# ------------------------------------------------------------------------------
# DNS_RESOLVERS -- servers used by lego to verify TXT record propagation
# Optional; defaults to Cloudflare (primary + secondary) and Google DNS.
# Empty value is treated as the default.
# ------------------------------------------------------------------------------
_v_dns="${DNS_RESOLVERS:-1.1.1.1:53,8.8.8.8:53,1.0.0.1:53}"
if [[ -z "${_v_dns}" ]]; then
    # Belt-and-suspenders: should not happen given the :- default above
    _vfail "DNS_RESOLVERS: must not be empty (default: 1.1.1.1:53,8.8.8.8:53,1.0.0.1:53)"
else
    VALIDATED_DNS_RESOLVERS="${_v_dns}"
fi

# ------------------------------------------------------------------------------
# UID / GID -- certificate file ownership
#
# bash declares $UID as a readonly built-in (current process UID).
# We use 'printenv' to read the value from the *process environment* directly,
# bypassing bash's variable namespace.  This correctly returns the value set by
# 'docker run --env UID=...' or '--env-file' without conflict.
# ------------------------------------------------------------------------------
_v_raw_uid="$(printenv UID 2>/dev/null || true)"
_v_raw_gid="$(printenv GID 2>/dev/null || true)"

CERT_UID="${_v_raw_uid:-1000}"
CERT_GID="${_v_raw_gid:-1000}"

if ! [[ "${CERT_UID}" =~ ^[0-9]+$ ]]; then
    _vfail "UID: must be a non-negative integer, got '${CERT_UID}'"
elif [[ "${CERT_UID}" -eq 0 ]]; then
    _vfail "UID: running lego as root (UID 0) is not permitted"
fi

if ! [[ "${CERT_GID}" =~ ^[0-9]+$ ]]; then
    _vfail "GID: must be a non-negative integer, got '${CERT_GID}'"
fi

# ------------------------------------------------------------------------------
# Report all collected errors at once, then return failure to the sourcing script
# ------------------------------------------------------------------------------
if [[ "${#_ERRORS[@]}" -gt 0 ]]; then
    log_err "Environment validation failed (${#_ERRORS[@]} error(s)):"
    for _v_e in "${_ERRORS[@]}"; do
        log_err "  ${_v_e}"
    done
    unset _ERRORS _v_tz _v_email _v_prod_raw _v_prod _v_tos_raw _v_tos
    unset _v_cf_key _v_klen _cf_secret_path _v_prop _v_dns
    unset _v_raw_uid _v_raw_gid _v_e
    unset -f _vfail
    return 1
fi

# Export validated variables for subsequent use in entrypoint.sh and run_lego.sh
export VALIDATED_TZ VALIDATED_EMAIL VALIDATED_PRODUCTION ACME_SERVER
export VALIDATED_DOMAINS DOMAINS_SOURCE VALIDATED_CF_KEY VALIDATED_PROPAGATION
export VALIDATED_DNS_RESOLVERS CERT_UID CERT_GID

# Clean up internal variables
unset _ERRORS _v_tz _v_email _v_prod_raw _v_prod _v_tos_raw _v_tos
unset _v_cf_key _v_klen _cf_secret_path _v_prop _v_dns
unset _v_raw_uid _v_raw_gid
unset -f _vfail
