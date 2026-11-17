#!/bin/sh
# Read the complete YAML before calling lego once per selected certificate.
set -efu
umask 077

case "$0" in
    */*) script_dir=${0%/*} ;;
    *) script_dir=. ;;
esac
. "${script_dir}/validate_env.sh"

# Return 1 for a failed lego invocation and 2 for a group that never reached lego.
run_group() (
    target=$(certgen-core output-path "$CERTGEN_ACTION" "$CERTGEN_OUTPUT" "$group") || return 2

    # Allow the full propagation wait for every name, plus ten minutes, and at
    # least one hour. This bounds runs where lego would sleep for many hours.
    if [ "$VALIDATED_ORDER_TIMEOUT" = auto ]; then
        time_limit=$((600 + $# * VALIDATED_PROPAGATION))
        [ "$time_limit" -ge 3600 ] || time_limit=3600
    else
        time_limit=$VALIDATED_ORDER_TIMEOUT
    fi

    log_section "🚀 $group"
    log_info "Output path: $target"
    if [ "$time_limit" -eq 0 ]; then
        log_info "Time limit: none"
    else
        log_info "Time limit: ${time_limit}s"
    fi
    log_info "Domains ($#):"
    for domain do
        log_step "$domain"
    done
    [ "$CERTGEN_ACTION" = issue ] || return 0

    # Rotate each domain into a pair of argv entries. The parser validates
    # the names and the shell keeps every wildcard literal.
    remaining=$#
    saved_ifs=$IFS
    IFS=,
    for resolver in $VALIDATED_DNS_RESOLVERS; do
        set -- "$@" --dns.resolvers "$resolver"
    done
    IFS=$saved_ifs
    while [ "$remaining" -gt 0 ]; do
        domain=$1
        shift
        set -- "$@" --domains "$domain"
        remaining=$((remaining - 1))
    done
    lego_status=0
    certgen-core supervise "$time_limit" \
        lego --accept-tos --email "$VALIDATED_EMAIL" --dns cloudflare --server "$ACME_SERVER" \
            --path "$target" "$@" run || lego_status=$?
    case "$lego_status" in
        0)
            log_ok "Certificates: $target/certificates/"
            log_ok "Account data: $target/accounts/"
            return 0
            ;;
        75) log_err "$group: Let's Encrypt rate limit reached. Retry after the time shown above." ;;
        124) log_err "$group: lego stopped after the ${time_limit}s time limit (ORDER_TIMEOUT_SECONDS)." ;;
        *) log_err "$group: lego exited with code $lego_status." ;;
    esac
    return 1
)

prepare_issuance() {
    configure_cloudflare /run/secrets/cloudflare_api_token || return
    if [ ! -d "$CERTGEN_OUTPUT" ] || [ ! -w "$CERTGEN_OUTPUT" ] || [ ! -x "$CERTGEN_OUTPUT" ]; then
        log_err "Output directory must be writable by UID $CERT_UID and GID $CERT_GID: $CERTGEN_OUTPUT"
        return 1
    fi
    if ! command -v lego >/dev/null; then
        log_err "lego is missing. Rebuild the image."
        return 1
    fi
}

groups=$(certgen-core domains "$CERTGEN_CONFIG" "$@")
setup_status=0
case "$CERTGEN_ACTION" in
    list) ;;
    dry-run) validate_env || setup_status=$? ;;
    issue) validate_env && prepare_issuance || setup_status=$? ;;
esac

if [ "$CERTGEN_ACTION" != list ] && [ "$setup_status" -eq 0 ]; then
    export TZ="$VALIDATED_TZ"
    if [ "$VALIDATED_PRODUCTION" = true ]; then
        log_section "🚨 PRODUCTION MODE"
    else
        log_section "🧪 STAGING MODE"
    fi
    log_info "ACME server: $ACME_SERVER"
    log_info "Cloudflare DNS-01 validates each name with a temporary TXT record."
    log_info "Timezone: $TZ"
    log_info "Target UID: $CERT_UID  GID: $CERT_GID"
fi

tab=$(printf '\t')
report=
exit_status=$setup_status
while IFS="$tab" read -r group domains <&3; do
    saved_ifs=$IFS
    IFS=,
    # No expansion beyond splitting on commas. DNS names are validated by C++.
    set -- $domains
    IFS=$saved_ifs
    if [ "$CERTGEN_ACTION" = list ]; then
        printf '%s: %s domains\n' "$group" "$#"
        continue
    fi

    result=2
    if [ "$setup_status" -eq 0 ]; then
        result=0
        run_group "$@" 3<&- || result=$?
        if [ "$result" -ne 0 ]; then
            exit_status=1
        fi
    fi
    report="$report$group$tab$#$tab$result
"
done 3<<GROUPS
$groups
GROUPS

if [ "$CERTGEN_ACTION" = issue ]; then
    log_section "📋 Certificate request summary (DNS names per certificate)"
    printf '%s' "$report" | certgen-core summary
fi
exit "$exit_status"
