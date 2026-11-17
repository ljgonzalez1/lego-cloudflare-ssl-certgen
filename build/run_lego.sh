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

# Return 1 for a failed lego invocation and 2 if it could not be started.
run_group() {
    local group="$1" domains="$2"
    local parent="${CERTGEN_OUTPUT%/}/${group}"
    local epoch stamp requested expires target domain
    local -a domain_list domain_flags=()

    IFS=',' read -ra domain_list <<< "${domains}"
    epoch="$(date +%s)" || return 2
    stamp="$(make_timestamp "${epoch}")" || return 2

    if [[ "${CERTGEN_ACTION}" == issue ]]; then
        if [[ -L "${parent}" ]] || ! mkdir -p -- "${parent}"; then
            log_err "Cannot use certificate folder: ${parent}"
            return 2
        fi
        # Reserve a unique timestamp folder even if two runs start together.
        until mkdir -- "${parent}/${stamp}" 2>/dev/null; do
            if [[ ! -e "${parent}/${stamp}" && ! -L "${parent}/${stamp}" ]]; then
                log_err "Cannot create timestamp folder: ${parent}/${stamp}"
                return 2
            fi
            epoch="$((epoch + 1))"
            stamp="$(make_timestamp "${epoch}")" || return 2
        done
    fi

    requested="$(make_timestamp "$((epoch + 55))")" || return 2
    expires="$(make_timestamp "$((epoch + 90 * 86400 - 12 * 3600))")" || return 2
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

    mkdir -- "${target}" || return 2
    if lego "${lego_args[@]}" --path "${target}" "${domain_flags[@]}" run; then
        log_ok "Certificates: ${target}/certificates/"
        log_ok "Account data: ${target}/accounts/"
    else
        log_err "${group}: lego exited with code $?."
        return 1
    fi
}

prepare_issuance() {
    configure_cloudflare || return
    if ! [[ -d "${CERTGEN_OUTPUT}" && -w "${CERTGEN_OUTPUT}" && -x "${CERTGEN_OUTPUT}" ]]; then
        log_err "Output directory must be writable by UID ${CERT_UID} and GID ${CERT_GID}: ${CERTGEN_OUTPUT}"
        return 1
    fi
    if ! command -v lego >/dev/null; then
        log_err "lego is missing. Rebuild the image."
        return 1
    fi
}

summary_row() {
    local width="$1" name="$2" attempted="$3" issued="$4" failed="$5" skipped="$6" status="$7" color="$8"
    printf '│ %-*s │ %9s │ %6s │ %6s │ %7s │ %s%-7s%s │\n' \
        "${width}" "${name}" "${attempted}" "${issued}" "${failed}" "${skipped}" "${color}" "${status}" "${_C_RST}"
}

print_summary() {
    local width=11 rule group domains index=0 count status color
    local attempted issued failed skipped total_attempted=0 total_issued=0 total_failed=0 total_skipped=0
    local issued_certificates=0 failed_certificates=0 skipped_certificates=0
    local -a domain_list

    while IFS=$'\t' read -r group domains; do
        if (( ${#group} > width )); then
            width="${#group}"
        fi
    done <<< "${groups}"

    printf -v rule '%*s' "$((width + 2))" ''
    rule="${rule// /─}"
    log_section "📋 Certificate request summary (DNS names per certificate)"
    printf '%s┌%s┬───────────┬────────┬────────┬─────────┬─────────┐%s\n' "${_C_CYN}" "${rule}" "${_C_RST}"
    printf '%s' "${_C_BOLD}"
    summary_row "${width}" Certificate Attempted Issued Failed Skipped Status ''
    printf '%s├%s┼───────────┼────────┼────────┼─────────┼─────────┤%s\n' "${_C_CYN}" "${rule}" "${_C_RST}"

    while IFS=$'\t' read -r group domains; do
        IFS=',' read -ra domain_list <<< "${domains}"
        count="${#domain_list[@]}"
        attempted=0 issued=0 failed=0 skipped=0
        # lego issues all requested names on a single certificate or the order fails.
        case "${group_results[index]:-2}" in
            0)
                attempted="${count}" issued="${count}" status=ISSUED color="${_C_GRN}"
                issued_certificates="$((issued_certificates + 1))"
                ;;
            1)
                attempted="${count}" failed="${count}" status=FAILED color="${_C_RED}"
                failed_certificates="$((failed_certificates + 1))"
                ;;
            *)
                skipped="${count}" status=SKIPPED color="${_C_YLW}"
                skipped_certificates="$((skipped_certificates + 1))"
                ;;
        esac

        summary_row "${width}" "${group}" "${attempted}" "${issued}" "${failed}" "${skipped}" "${status}" "${color}"
        total_attempted="$((total_attempted + attempted))"
        total_issued="$((total_issued + issued))"
        total_failed="$((total_failed + failed))"
        total_skipped="$((total_skipped + skipped))"
        index="$((index + 1))"
    done <<< "${groups}"

    printf '%s├%s┼───────────┼────────┼────────┼─────────┼─────────┤%s\n' "${_C_CYN}" "${rule}" "${_C_RST}"
    printf '%s' "${_C_BOLD}"
    summary_row "${width}" TOTAL "${total_attempted}" "${total_issued}" "${total_failed}" "${total_skipped}" '' ''
    printf '%s└%s┴───────────┴────────┴────────┴─────────┴─────────┘%s\n' "${_C_CYN}" "${rule}" "${_C_RST}"
    log_info "Certificates: $((issued_certificates + failed_certificates)) attempted, ${issued_certificates} issued, ${failed_certificates} failed, ${skipped_certificates} skipped."
}

groups="$(python3 "${script_dir}/read_domains.py" "${CERTGEN_CONFIG}" "$@")"
if [[ "${CERTGEN_ACTION}" == list ]]; then
    while IFS=$'\t' read -r group domains; do
        IFS=',' read -ra domain_list <<< "${domains}"
        printf '%s: %s domains\n' "${group}" "${#domain_list[@]}"
    done <<< "${groups}"
    exit 0
fi

group_results=()
setup_status=0
validate_env || setup_status=$?
if [[ "${CERTGEN_ACTION}" == issue ]] && (( setup_status == 0 )); then
    prepare_issuance || setup_status=$?
fi
if (( setup_status != 0 )); then
    if [[ "${CERTGEN_ACTION}" == issue ]]; then
        print_summary
    fi
    exit "${setup_status}"
fi
export TZ="${VALIDATED_TZ}"

if [[ "${VALIDATED_PRODUCTION}" == true ]]; then
    log_section "🚨 PRODUCTION MODE"
else
    log_section "🧪 STAGING MODE"
fi
log_info "ACME server: ${ACME_SERVER}"
if [[ "${VALIDATED_DISABLE_CNAME_SUPPORT}" == true ]]; then
    log_info "DNS-01 uses TXT at the exact _acme-challenge name."
else
    log_info "DNS-01 follows CNAME delegations at _acme-challenge."
fi
log_info "Timezone: ${TZ}"
log_info "Target UID: ${CERT_UID}  GID: ${CERT_GID}"

lego_args=(--accept-tos --email "${VALIDATED_EMAIL}" --dns cloudflare --server "${ACME_SERVER}")
for resolver in "${VALIDATED_DNS_RESOLVERS[@]}"; do
    lego_args+=(--dns.resolvers "${resolver}")
done

exit_status=0
while IFS=$'\t' read -r -u 3 group domains; do
    result=0
    run_group "${group}" "${domains}" || result=$?
    group_results+=("${result}")
    if (( result != 0 )); then
        exit_status=1
    fi
done 3<<< "${groups}"

if [[ "${CERTGEN_ACTION}" == issue ]]; then
    print_summary
fi
exit "${exit_status}"
