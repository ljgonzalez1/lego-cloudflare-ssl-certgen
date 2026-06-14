#!/usr/bin/env bash
# ==============================================================================
# run_lego.sh  --  Certificate request execution  (runs as certgen, non-root)
# ==============================================================================
# Called by entrypoint.sh via:  exec gosu <uid>:<gid> /usr/local/bin/run_lego.sh
#
# All inputs arrive as exported environment variables set by entrypoint.sh:
#   CF_DNS_API_TOKEN              -- Cloudflare DNS API token (for lego)
#   CLOUDFLARE_PROPAGATION_TIMEOUT -- propagation wait (seconds, for lego)
#   ACME_SERVER                   -- Let's Encrypt directory URL
#   VALIDATED_EMAIL               -- account email
#   VALIDATED_DOMAINS             -- comma-separated domain list
#   VALIDATED_DNS_RESOLVERS       -- comma-separated host:port resolvers
#   VALIDATED_TZ                  -- timezone (already set as TZ by entrypoint)
#   CERT_OUTPUT_DIR               -- absolute path for this run's output
# ==============================================================================
set -euo pipefail

# ------------------------------------------------------------------------------
# Terminal detection and ANSI colour codes
# ------------------------------------------------------------------------------
if [[ -t 1 ]]; then
    _C_RST=$'\033[0m'
    _C_BOLD=$'\033[1m'
    _C_RED=$'\033[31m'
    _C_GRN=$'\033[32m'
    _C_YLW=$'\033[33m'
    _C_BLU=$'\033[34m'
    _C_MAG=$'\033[35m'
    _C_CYN=$'\033[36m'
else
    _C_RST='' _C_BOLD=''
    _C_RED='' _C_GRN='' _C_YLW='' _C_BLU='' _C_MAG='' _C_CYN=''
fi

log_info()    { printf '%s[*]  %s%s\n'  "${_C_CYN}"            "$*" "${_C_RST}";     }
log_step()    { printf '%s[>]  %s%s\n'  "${_C_BLU}"            "$*" "${_C_RST}";     }
log_ok()      { printf '%s[OK] %s%s\n'  "${_C_GRN}"            "$*" "${_C_RST}";     }
log_warn()    { printf '%s[!]  %s%s\n'  "${_C_YLW}"            "$*" "${_C_RST}";     }
log_err()     { printf '%s[X]  %s%s\n'  "${_C_RED}"            "$*" "${_C_RST}" >&2; }
log_section() { printf '\n%s=== %s ===%s\n' "${_C_BOLD}${_C_MAG}" "$*" "${_C_RST}"; }

# ------------------------------------------------------------------------------
# Internal sanity check: all required vars must be set.
# These should always be set by entrypoint.sh; this guard catches internal bugs.
# ------------------------------------------------------------------------------
_required_vars=(
    CF_DNS_API_TOKEN
    CLOUDFLARE_PROPAGATION_TIMEOUT
    ACME_SERVER
    VALIDATED_EMAIL
    VALIDATED_DOMAINS
    CERT_OUTPUT_DIR
)
_internal_errors=()
for _rv in "${_required_vars[@]}"; do
    if [[ -z "${!_rv:-}" ]]; then
        _internal_errors+=("${_rv}")
    fi
done
if [[ "${#_internal_errors[@]}" -gt 0 ]]; then
    log_err "Internal error: the following variables were not passed by entrypoint.sh:"
    for _ie in "${_internal_errors[@]}"; do
        log_err "  ${_ie}"
    done
    exit 1
fi

# ------------------------------------------------------------------------------
# Build the --domains flags
# Re-parse the validated comma-separated string into individual flags.
# ------------------------------------------------------------------------------
_domain_flags=()
_domain_count=0
IFS=',' read -ra _dom_list <<< "${VALIDATED_DOMAINS}"
for _d in "${_dom_list[@]}"; do
    _d="${_d// /}"                  # strip any residual whitespace
    if [[ -n "${_d}" ]]; then
        _domain_flags+=("--domains" "${_d}")
        _domain_count=$(( _domain_count + 1 ))
    fi
done

if [[ "${_domain_count}" -eq 0 ]]; then
    log_err "Internal error: parsed zero domains from VALIDATED_DOMAINS='${VALIDATED_DOMAINS}'"
    exit 1
fi

# ------------------------------------------------------------------------------
# Build the --dns.resolvers flags
# Each resolver is passed as a separate flag for maximum compatibility.
# ------------------------------------------------------------------------------
_resolver_flags=()
if [[ -n "${VALIDATED_DNS_RESOLVERS:-}" ]]; then
    IFS=',' read -ra _res_list <<< "${VALIDATED_DNS_RESOLVERS}"
    for _r in "${_res_list[@]}"; do
        _r="${_r// /}"
        [[ -n "${_r}" ]] && _resolver_flags+=("--dns.resolvers" "${_r}")
    done
fi

# ------------------------------------------------------------------------------
# Summary
# ------------------------------------------------------------------------------
log_info "Requesting certificate for ${_domain_count} domain(s):"
for _d in "${_dom_list[@]}"; do
    _d="${_d// /}"
    [[ -n "${_d}" ]] && log_step "  ${_d}"
done
printf '\n'
log_info "ACME server  : ${ACME_SERVER}"
log_info "Output path  : ${CERT_OUTPUT_DIR}"

# ------------------------------------------------------------------------------
# Build complete lego argument array
# ------------------------------------------------------------------------------
_lego_args=(
    "--accept-tos"
    "--email"    "${VALIDATED_EMAIL}"
    "--dns"      "cloudflare"
    "--server"   "${ACME_SERVER}"
    "--path"     "${CERT_OUTPUT_DIR}"
)

# Append resolver flags (may be empty)
if [[ "${#_resolver_flags[@]}" -gt 0 ]]; then
    _lego_args+=("${_resolver_flags[@]}")
fi

# Append domain flags
_lego_args+=("${_domain_flags[@]}")

# ------------------------------------------------------------------------------
# Execute lego
# CF_DNS_API_TOKEN and CLOUDFLARE_PROPAGATION_TIMEOUT are already in the
# environment; lego reads them automatically for the cloudflare provider.
# ------------------------------------------------------------------------------
printf '\n'
if lego "${_lego_args[@]}" run; then
    printf '\n'
    log_ok "Certificate successfully generated!"
    log_ok "Certificates : ${CERT_OUTPUT_DIR}/certificates/"
    log_ok "Account data : ${CERT_OUTPUT_DIR}/accounts/"
    printf '\n'
else
    _rc=$?
    printf '\n'
    log_err "lego exited with error code ${_rc}."
    log_err "Review the lego output above for details."
    log_err "Common causes:"
    log_err "  - DNS TXT record propagation timeout (increase PROPAGATION_SECONDS)"
    log_err "  - Invalid or insufficient Cloudflare API token permissions"
    log_err "  - Domain not in Cloudflare DNS"
    log_err "  - Let's Encrypt rate limit reached (use PRODUCTION=false for testing)"
    exit "${_rc}"
fi
