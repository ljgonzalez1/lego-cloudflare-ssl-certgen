#!/usr/bin/env bash
# ==============================================================================
# entrypoint.sh  --  Container entry point  (runs as root)
# ==============================================================================
# Phase 1 (root):
#   1. Dependency pre-flight (lego, gosu, cloudflare provider)
#   2. Source validate_env.sh  -- validates all env vars, sets VALIDATED_* vars
#      (validate_env.sh sources validate_domains.sh for the domain handling)
#   3. Announce mode (staging / production)
#   4. Adjust certgen user to requested UID/GID
#   5. Prepare output directory with correct ownership
#   6. Verify write access as target user
#   7. Export all vars required by run_lego.sh
#   8. exec gosu -> run_lego.sh  (privilege drop, root -> certgen)
#
# Phase 2 (certgen, non-root):
#   run_lego.sh executes lego and reports success / failure.
#
# All console output is plain 7-bit ASCII plus ANSI colour escapes only.
# ==============================================================================
set -euo pipefail

# ------------------------------------------------------------------------------
# Terminal detection and ANSI colour codes (pure ASCII escape sequences)
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

log_banner() {
    printf '%s' "${_C_BOLD}${_C_CYN}"
    printf '%s\n' \
        '+============================================================+' \
        '|                 lego-cloudflare-certgen                    |' \
        '|   SSL/TLS certificate generation via ACME + Cloudflare     |' \
        '+============================================================+'
    printf '%s' "${_C_RST}"
}

# ------------------------------------------------------------------------------
# Timestamp  ->  "YYYY-MM-DD_HH.MM.SS_GMT[+-]H"
# Uses the *validated* TZ at call time.  Called after validate_env.sh has run.
# ------------------------------------------------------------------------------
make_timestamp() {
    local tz="${1:-${VALIDATED_TZ:-Etc/UTC}}"
    local epoch offset_raw sign hours
    epoch="$(TZ="${tz}" date +%s)"
    offset_raw="$(TZ="${tz}" date --date="@${epoch}" +%z)"   # e.g. -0300 or +0530
    sign="${offset_raw:0:1}"                                  # + or -
    hours="$((10#${offset_raw:1:2}))"                         # strip leading zero
    TZ="${tz}" date --date="@${epoch}" +"%Y-%m-%d_%H.%M.%S_GMT${sign}${hours}"
}

# ==============================================================================
# PHASE 1 -- root
# ==============================================================================

log_banner

log_section "Dependency checks"

# -- required binaries ---------------------------------------------------------
_missing=()
command -v lego  >/dev/null 2>&1 || _missing+=("lego")
command -v gosu  >/dev/null 2>&1 || _missing+=("gosu")
command -v findmnt >/dev/null 2>&1 || _missing+=("findmnt (util-linux)")

if [[ "${#_missing[@]}" -gt 0 ]]; then
    log_err "Missing required tool(s): ${_missing[*]}"
    log_err "This should not happen in the official image. Please rebuild."
    exit 1
fi

# -- cloudflare DNS provider availability -------------------------------------
if ! lego dnshelp 2>&1 | grep -qw "cloudflare"; then
    log_err "This lego binary does not support the 'cloudflare' DNS provider."
    log_err "Download the correct release from:"
    log_err "  https://github.com/go-acme/lego/releases"
    exit 1
fi

log_ok "lego    : $(lego --version 2>&1 | head -1)"
log_ok "gosu    : $(gosu --version 2>&1 | tr -d '\n')"
log_ok "cloudflare DNS provider: available"

# ==============================================================================
# Environment validation (sets VALIDATED_* and CERT_UID / CERT_GID)
# ==============================================================================
log_section "Validating environment"

# shellcheck source=validate_env.sh
. /usr/local/bin/validate_env.sh

# ==============================================================================
# Mode announcement
# ==============================================================================
if [[ "${VALIDATED_PRODUCTION}" == "true" ]]; then
    log_section "PRODUCTION MODE"
    log_warn "Issuing REAL, browser-trusted certificates via Let's Encrypt."
    log_warn "Rate limit: 5 duplicate certificates per registered domain per week."
else
    log_section "STAGING MODE"
    log_warn "Using Let's Encrypt STAGING — certificates will NOT be trusted by browsers."
    log_warn "Relaxed rate limits apply.  Safe for testing and development."
fi

log_info "ACME server  : ${ACME_SERVER}"
log_info "Email        : ${VALIDATED_EMAIL}"
log_info "Timezone     : ${VALIDATED_TZ}"
log_info "Propagation  : ${VALIDATED_PROPAGATION}s"
log_info "DNS resolvers: ${VALIDATED_DNS_RESOLVERS}"
log_info "Domains src  : ${DOMAINS_SOURCE:-file /domains.txt}"
log_info "Domains      : ${VALIDATED_DOMAINS}"
log_info "Target UID   : ${CERT_UID}  GID: ${CERT_GID}"

# ==============================================================================
# Volume / mount check
# ==============================================================================
log_section "Volume check"

if findmnt -M /ssl-certs >/dev/null 2>&1; then
    log_ok "/ssl-certs is a mounted volume"
else
    log_warn "/ssl-certs is NOT a mounted volume."
    log_warn "Certificates will be generated inside the container and LOST when it exits."
    log_warn "Pass:  --volume /host/path:/ssl-certs"
fi

# ==============================================================================
# Certgen user setup  (adjust UID/GID to match requested values)
# ==============================================================================
log_section "User setup"

# Check for UID collision with an existing system user other than 'certgen'
_uid_owner="$(getent passwd "${CERT_UID}" | cut -d: -f1 2>/dev/null || true)"
if [[ -n "${_uid_owner}" && "${_uid_owner}" != "certgen" ]]; then
    log_err "UID ${CERT_UID} is already used by system user '${_uid_owner}'."
    log_err "Choose a different UID in your .env (current: UID=${CERT_UID})."
    exit 1
fi

_gid_owner="$(getent group "${CERT_GID}" | cut -d: -f1 2>/dev/null || true)"
if [[ -n "${_gid_owner}" && "${_gid_owner}" != "certgen" ]]; then
    log_err "GID ${CERT_GID} is already used by group '${_gid_owner}'."
    log_err "Choose a different GID in your .env (current: GID=${CERT_GID})."
    exit 1
fi

# Adjust certgen GID if needed (must change group before user)
_cur_gid="$(id -g certgen 2>/dev/null || echo '')"
if [[ "${_cur_gid}" != "${CERT_GID}" ]]; then
    log_step "Adjusting certgen GID: ${_cur_gid} -> ${CERT_GID}"
    groupmod --gid "${CERT_GID}" certgen
fi

# Adjust certgen UID if needed
_cur_uid="$(id -u certgen 2>/dev/null || echo '')"
if [[ "${_cur_uid}" != "${CERT_UID}" ]]; then
    log_step "Adjusting certgen UID: ${_cur_uid} -> ${CERT_UID}"
    usermod --uid "${CERT_UID}" certgen
fi

log_ok "certgen user: UID=$(id -u certgen)  GID=$(id -g certgen)"

# ==============================================================================
# Output directory preparation
# ==============================================================================
log_section "Preparing output directory"

CERT_OUTPUT_DIR="/ssl-certs/$(make_timestamp "${VALIDATED_TZ}")"
export CERT_OUTPUT_DIR

log_step "Output path: ${CERT_OUTPUT_DIR}"

mkdir -p "${CERT_OUTPUT_DIR}"
# Give certgen ownership of both the top-level /ssl-certs and the timestamped dir
chown "${CERT_UID}:${CERT_GID}" /ssl-certs "${CERT_OUTPUT_DIR}"

# Verify write access as the target user (not as root)
if ! gosu "${CERT_UID}:${CERT_GID}" test -w "${CERT_OUTPUT_DIR}"; then
    log_err "Write permission check FAILED."
    log_err "User UID=${CERT_UID} cannot write to ${CERT_OUTPUT_DIR}."
    log_err "Options:"
    log_err "  1. Ensure the host volume directory is owned by UID ${CERT_UID}:"
    log_err "       chown ${CERT_UID}:${CERT_GID} /host/path/to/ssl-certs"
    log_err "  2. Use a UID/GID that matches the host directory owner."
    exit 1
fi

log_ok "Write access verified for UID ${CERT_UID} on ${CERT_OUTPUT_DIR}"

# ==============================================================================
# Export all variables needed by run_lego.sh
# (gosu preserves the process environment, so these carry through exec)
# ==============================================================================
export TZ="${VALIDATED_TZ}"
export CF_DNS_API_TOKEN="${VALIDATED_CF_KEY}"
export CLOUDFLARE_PROPAGATION_TIMEOUT="${VALIDATED_PROPAGATION}"
# ACME_SERVER, VALIDATED_EMAIL, VALIDATED_DOMAINS, VALIDATED_DNS_RESOLVERS,
# VALIDATED_TZ, CERT_OUTPUT_DIR are already exported above.

log_section "Starting certificate request (dropping to UID ${CERT_UID})"
exec gosu "${CERT_UID}:${CERT_GID}" /usr/local/bin/run_lego.sh
