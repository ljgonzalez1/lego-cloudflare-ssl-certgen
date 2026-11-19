#!/bin/sh
# Read the complete YAML before calling lego once per selected certificate.
set -efu
umask 077

case "$0" in
    */*) script_dir=${0%/*} ;;
    *) script_dir=. ;;
esac
. "${script_dir}/validate_env.sh"

# Return 1 when no certificate was saved and 2 for a group that never reached lego.
run_group() (
    target=$(certgen-core output-path "$CERTGEN_ACTION" "$CERTGEN_OUTPUT" "$group") || return 2
    # certgen-core estimates the longest normal run, adds 50% and caps it at two hours.
    time_limit=$(certgen-core time-limit "$#" "$VALIDATED_PROPAGATION") || return 2
    first_name=$1

    log_section "🚀 $group"
    log_info "Output path: $target"
    log_info "Time limit: ${time_limit}s ($(((time_limit + 59) / 60)) min)"
    log_info "Domains ($#):"
    # The live view draws the list itself and updates it while lego works.
    if [ "$CERTGEN_ACTION" != issue ] || [ "$VALIDATED_LEGO_OUTPUT" = raw ]; then
        for domain do
            log_step "$domain"
        done
    fi
    [ "$CERTGEN_ACTION" = issue ] || return 0

    # An empty --view keeps lego's own output.
    view_names=
    if [ "$VALIDATED_LEGO_OUTPUT" = pretty ]; then
        saved_ifs=$IFS
        IFS=,
        view_names="$*"
        IFS=$saved_ifs
    fi

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
    # lego replaces the leading "*" in a wildcard file name with "_".
    case "$first_name" in
        '*.'*) base="_${first_name#\*}" ;;
        *) base=$first_name ;;
    esac
    if certgen-core supervise --log "$target/lego.log" --view "$view_names" \
        --certificate "$target/certificates/$base" --group "$group" "$time_limit" \
        lego --accept-tos --email "$VALIDATED_EMAIL" --dns cloudflare --server "$ACME_SERVER" \
            --path "$target" "$@" run; then
        log_ok "Certificates: $target/certificates/"
        log_ok "Account data: $target/accounts/"
        return 0
    fi
    log_info "Full lego output: $target/lego.log"
    return 1
)

prepare_issuance() {
    credential_status=0
    configure_cloudflare /run/secrets/cloudflare_api_token || credential_status=$?
    case "$credential_status" in
        0) ;;
        3) setup_result=1; return 2 ;; # The configured token was rejected locally.
        *) return "$credential_status" ;;
    esac
    if [ ! -d "$CERTGEN_OUTPUT" ] || [ ! -w "$CERTGEN_OUTPUT" ] || [ ! -x "$CERTGEN_OUTPUT" ]; then
        log_err "Output directory must be writable by UID $CERT_UID and GID $CERT_GID: $CERTGEN_OUTPUT"
        return 1
    fi
    if ! command -v lego >/dev/null; then
        log_err "lego is missing. Rebuild the image."
        return 1
    fi
}

if [ "$CERTGEN_ACTION" = issue ]; then
    groups=$(certgen-core domains --issue "$CERTGEN_CONFIG" "$@")
else
    groups=$(certgen-core domains "$CERTGEN_CONFIG" "$@")
fi
setup_status=0
setup_result=2
test_mode=false
case "$CERTGEN_ACTION" in
    list) ;;
    test)
        test_mode=true
        CERTGEN_ACTION=dry-run
        PRODUCTION=false
        validate_env || exit $?
        if ! lego_version=$(certgen-core supervise 10 lego --version </dev/null 2>&1); then
            log_err "Could not check the lego executable."
            exit 1
        fi
        case "$lego_version" in
            'lego version '*) ;;
            *) log_err "Unexpected response from lego --version."; exit 1 ;;
        esac
        log_section "🧪 TEST MODE (offline)"
        log_info "$lego_version"
        ;;
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
while IFS="$tab" read -r group domains oversized_count <&3; do
    saved_ifs=$IFS
    IFS=,
    # No expansion beyond splitting on commas. DNS names are validated by C++.
    set -- $domains
    IFS=$saved_ifs
    if [ "$CERTGEN_ACTION" = list ]; then
        printf '%s: %s domains\n' "$group" "$#"
        continue
    fi

    count=$#
    result=$setup_result
    if [ -n "$oversized_count" ]; then
        count=$oversized_count
        result=1
        log_err "$group: $count DNS names exceed the 100-name limit. Split them into smaller certificate groups."
    elif [ "$setup_status" -eq 0 ]; then
        result=0
        run_group "$@" 3<&- || result=$?
    fi
    if [ "$result" -ne 0 ] && [ "$exit_status" -eq 0 ]; then
        exit_status=1
    fi
    report="$report$group$tab$count$tab$result
"
done 3<<GROUPS
$groups
GROUPS

if [ "$CERTGEN_ACTION" = issue ]; then
    log_section "📋 Certificate request summary (DNS names per certificate)"
    printf '%s' "$report" | certgen-core summary
fi
if [ "$test_mode" = true ] && [ "$exit_status" -eq 0 ]; then
    log_ok "Offline test passed."
fi
exit "$exit_status"
