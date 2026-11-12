#!/usr/bin/env bash
# ==============================================================================
# run_lego.sh  --  Certificate requests  (runs as the target UID/GID)
# ==============================================================================
# Reads the complete YAML before calling lego once per selected group.
# ==============================================================================
set -euo pipefail
umask 077

script_dir="$(dirname -- "${BASH_SOURCE[0]}")"

# shellcheck source=validate_env.sh
. "${script_dir}/validate_env.sh"

# Preserve the original YYYY-MM-DD_HH.MM.SS_GMT[+-]H folder names.
make_timestamp() {
    local epoch="$1" offset hours
    offset="$(date --date="@${epoch}" +%z)" || return
    hours="$((10#${offset:1:2}))"
    date --date="@${epoch}" +"%Y-%m-%d_%H.%M.%S_GMT${offset:0:1}${hours}"
}

run_group() {
    local group="$1" domains="$2"
    local parent="${CERTGEN_OUTPUT%/}/${group}"
    local epoch stamp requested expires target domain
    local -a domain_list domain_flags=()

    IFS=',' read -ra domain_list <<< "${domains}"
    epoch="$(date +%s)" || return
    stamp="$(make_timestamp "${epoch}")" || return

    if [[ "${CERTGEN_ACTION}" == issue ]]; then
        if [[ -L "${parent}" ]] || ! mkdir -p -- "${parent}"; then
            log_err "Cannot use certificate folder: ${parent}"
            return 1
        fi
        # Reserve a unique timestamp folder even if two runs start together.
        until mkdir -- "${parent}/${stamp}" 2>/dev/null; do
            if [[ ! -e "${parent}/${stamp}" && ! -L "${parent}/${stamp}" ]]; then
                log_err "Cannot create timestamp folder: ${parent}/${stamp}"
                return 1
            fi
            epoch="$((epoch + 1))"
            stamp="$(make_timestamp "${epoch}")" || return
        done
    fi

    requested="$(make_timestamp "$((epoch + 55))")" || return
    expires="$(make_timestamp "$((epoch + 90 * 86400 - 12 * 3600))")" || return
    target="${parent}/${stamp}/requested_${requested}_expires_${expires}"

    log_section "🚀 ${group}"
    log_info "Output path: ${target}"
    log_info "Domains (${#domain_list[@]}):"
    for domain in "${domain_list[@]}"; do
        log_step "${domain}"
        domain_flags+=(--domains "${domain}")
    done

    if [[ "${CERTGEN_ACTION}" == dry-run ]]; then
        return 0
    fi

    mkdir -- "${target}" || return
    if lego "${lego_args[@]}" --path "${target}" "${domain_flags[@]}" run; then
        log_ok "Certificates: ${target}/certificates/"
        log_ok "Account data: ${target}/accounts/"
    else
        log_err "${group}: lego exited with code $?."
        return 1
    fi
}

groups="$(python3 "${script_dir}/read_domains.py" "${CERTGEN_CONFIG}" "$@")"
if [[ "${CERTGEN_ACTION}" == list ]]; then
    while IFS=$'\t' read -r group domains; do
        IFS=',' read -ra domain_list <<< "${domains}"
        printf '%s: %s domains\n' "${group}" "${#domain_list[@]}"
    done <<< "${groups}"
    exit 0
fi

validate_env
export TZ="${VALIDATED_TZ}"

if [[ "${CERTGEN_ACTION}" == issue ]]; then
    configure_cloudflare
    if ! [[ -d "${CERTGEN_OUTPUT}" && -w "${CERTGEN_OUTPUT}" && -x "${CERTGEN_OUTPUT}" ]]; then
        log_err "Output directory must be writable by UID ${CERT_UID} and GID ${CERT_GID}: ${CERTGEN_OUTPUT}"
        exit 1
    fi
    if ! command -v lego >/dev/null; then
        log_err "lego is missing. Rebuild the image."
        exit 1
    fi
fi

if [[ "${VALIDATED_PRODUCTION}" == true ]]; then
    log_section "🚨 PRODUCTION MODE"
else
    log_section "🧪 STAGING MODE"
fi
log_info "ACME server: ${ACME_SERVER}"
log_info "Timezone: ${TZ}"
log_info "Target UID: ${CERT_UID}  GID: ${CERT_GID}"

lego_args=(--accept-tos --email "${VALIDATED_EMAIL}" --dns cloudflare --server "${ACME_SERVER}")
for resolver in "${VALIDATED_DNS_RESOLVERS[@]}"; do
    lego_args+=(--dns.resolvers "${resolver}")
done

succeeded=()
failed=()
while IFS=$'\t' read -r group domains; do
    if run_group "${group}" "${domains}"; then
        succeeded+=("${group}")
    else
        failed+=("${group}")
    fi
done <<< "${groups}"

if [[ "${CERTGEN_ACTION}" == issue ]]; then
    log_section "📋 Certificate request summary"
    for group in "${succeeded[@]}"; do
        log_ok "${group}"
    done
    for group in "${failed[@]}"; do
        log_err "${group}"
    done
fi
(( ${#failed[@]} == 0 ))
