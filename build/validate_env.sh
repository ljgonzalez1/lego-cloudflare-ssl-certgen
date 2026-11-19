#!/bin/sh
# Shared logging and environment validation.

if [ -t 1 ]; then
    escape=$(printf '\033')
    C_RESET="${escape}[0m" C_BOLD="${escape}[1m" C_RED="${escape}[31m"
    C_GREEN="${escape}[32m" C_YELLOW="${escape}[33m" C_BLUE="${escape}[34m"
    C_MAGENTA="${escape}[35m" C_CYAN="${escape}[36m"
else
    C_RESET='' C_BOLD='' C_RED='' C_GREEN='' C_YELLOW='' C_BLUE='' C_MAGENTA='' C_CYAN=''
fi

log_info()    { printf '%sℹ️  %s%s\n' "$C_CYAN" "$*" "$C_RESET"; }
log_step()    { printf '%s🔹 %s%s\n' "$C_BLUE" "$*" "$C_RESET"; }
log_ok()      { printf '%s✅ %s%s\n' "$C_GREEN" "$*" "$C_RESET"; }
log_warn()    { printf '%s⚠️  %s%s\n' "$C_YELLOW" "$*" "$C_RESET"; }
log_err()     { printf '%s❌ %s%s\n' "$C_RED" "$*" "$C_RESET" >&2; }
log_section() { printf '\n%s%s%s\n' "${C_BOLD}${C_MAGENTA}" "$*" "$C_RESET"; }

parse_uint() (
    case "$1" in
        ''|*[!0123456789]*) return 1 ;;
    esac
    [ "${#1}" -le "$2" ] || return 1
    number=$1
    while [ "${number#0}" != "$number" ]; do
        number=${number#0}
    done
    number=${number:-0}
    [ "$number" -le "$3" ] || return 1
    printf '%s' "$number"
)

validate_identity() {
    if ! CERT_UID=$(parse_uint "${CERT_UID:-${UID:-1000}}" 10 4294967294) || [ "$CERT_UID" -eq 0 ]; then
        log_err "UID must be an integer from 1 to 4294967294."
        return 2
    fi
    if ! CERT_GID=$(parse_uint "${CERT_GID:-${GID:-1000}}" 10 4294967294) || [ "$CERT_GID" -eq 0 ]; then
        log_err "GID must be an integer from 1 to 4294967294."
        return 2
    fi
    export CERT_UID CERT_GID
}

valid_email() {
    case "$1" in
        *[[:space:]]*|@*|*@*@*) return 1 ;;
        *@?*.?*) return 0 ;;
        *) return 1 ;;
    esac
}

valid_resolver() (
    case "$1" in
        *:*) host=${1%:*}; port=${1##*:} ;;
        *) return 1 ;;
    esac
    case "$host" in
        \[*\])
            address=${host#\[}
            address=${address%\]}
            case "$address" in
                ''|*[!0123456789abcdefABCDEF:.]*) return 1 ;;
            esac
            ;;
        ''|*[!a-zA-Z0-9_.-]*) return 1 ;;
    esac
    port=$(parse_uint "$port" 5 65535) && [ "$port" -gt 0 ]
)

invalid_setting() {
    if [ "$settings_status" -eq 0 ]; then
        log_err "Environment validation failed."
        settings_status=2
    fi
    printf '  %s\n' "$1" >&2
}

validate_env() {
    settings_status=0
    VALIDATED_TZ=${TZ:-Etc/UTC}
    case "$VALIDATED_TZ" in
        /*|*..*) invalid_setting "TZ: expected an IANA timezone such as Etc/UTC or America/Santiago." ;;
        *)
            if [ ! -f "/usr/share/zoneinfo/$VALIDATED_TZ" ]; then
                invalid_setting "TZ: expected an IANA timezone such as Etc/UTC or America/Santiago."
            fi
            ;;
    esac

    VALIDATED_EMAIL=${EMAIL:-}
    if [ -n "$VALIDATED_EMAIL" ] || [ "$CERTGEN_ACTION" = issue ]; then
        if ! valid_email "$VALIDATED_EMAIL"; then
            invalid_setting "EMAIL: enter a valid Let's Encrypt contact address."
        fi
    fi

    case "${PRODUCTION:-false}" in
        [Tt][Rr][Uu][Ee])
            VALIDATED_PRODUCTION=true
            ACME_SERVER=https://acme-v02.api.letsencrypt.org/directory
            ;;
        [Ff][Aa][Ll][Ss][Ee])
            VALIDATED_PRODUCTION=false
            ACME_SERVER=https://acme-staging-v02.api.letsencrypt.org/directory
            ;;
        *) invalid_setting "PRODUCTION: expected true or false." ;;
    esac

    if [ "$CERTGEN_ACTION" = issue ]; then
        case "${ACCEPT_LEGO_TOS:-false}" in
            [Tt][Rr][Uu][Ee]) ;;
            *) invalid_setting "ACCEPT_LEGO_TOS: set to true after reviewing https://letsencrypt.org/repository/" ;;
        esac
    fi

    if ! VALIDATED_PROPAGATION=$(parse_uint "${PROPAGATION_SECONDS:-360}" 10 2147483647); then
        invalid_setting "PROPAGATION_SECONDS: expected a non-negative integer in seconds."
    fi

    # auto shows the live view when stdout is a terminal and lego's own log otherwise.
    VALIDATED_LEGO_OUTPUT=${LEGO_OUTPUT:-auto}
    case "$VALIDATED_LEGO_OUTPUT" in
        auto)
            VALIDATED_LEGO_OUTPUT=raw
            [ ! -t 1 ] || VALIDATED_LEGO_OUTPUT=pretty
            ;;
        pretty|raw) ;;
        *) invalid_setting "LEGO_OUTPUT: expected auto, pretty or raw." ;;
    esac

    resolvers=${DNS_RESOLVERS:-1.1.1.1:53,8.8.8.8:53,1.0.0.1:53}
    VALIDATED_DNS_RESOLVERS=
    while [ "${resolvers#* }" != "$resolvers" ]; do
        VALIDATED_DNS_RESOLVERS=$VALIDATED_DNS_RESOLVERS${resolvers%% *}
        resolvers=${resolvers#* }
    done
    VALIDATED_DNS_RESOLVERS=$VALIDATED_DNS_RESOLVERS$resolvers
    case "$VALIDATED_DNS_RESOLVERS" in
        ''|,*|*,|*,,*|*[[:space:]]*)
            invalid_setting "DNS_RESOLVERS: expected comma-separated host:port entries."
            ;;
        *)
            saved_ifs=$IFS
            IFS=,
            for resolver in $VALIDATED_DNS_RESOLVERS; do
                if ! valid_resolver "$resolver"; then
                    invalid_setting "DNS_RESOLVERS: invalid resolver '$resolver'."
                fi
            done
            IFS=$saved_ifs
            ;;
    esac
    return "$settings_status"
}

read_secret() (
    line=
    while IFS= read -r line || [ -n "$line" ]; do
        printf '%s\n' "$line"
    done < "$1"
)

configure_cloudflare() {
    secret=$1
    token=${CLOUDFLARE_API_KEY:-${CF_DNS_API_TOKEN:-${CLOUDFLARE_API_TOKEN:-}}}
    use_file=false

    # Use the exact TXT owner for inherited wildcard aliases.
    # A legacy override can enable explicit DNS-01 CNAME delegation.
    case "${LEGO_DISABLE_CNAME_SUPPORT:-true}" in
        [Tt][Rr][Uu][Ee]|1|[Tt]) disable_cname_following=true ;;
        [Ff][Aa][Ll][Ss][Ee]|0|[Ff]) disable_cname_following=false ;;
        *) log_err "LEGO_DISABLE_CNAME_SUPPORT must be true or false."; return 2 ;;
    esac

    if [ -z "$token" ] && [ -r "$secret" ]; then
        token=$(read_secret "$secret") || return 2
        use_file=true
    fi
    case "$token" in
        ''|*[!a-zA-Z0-9_-]*)
            log_err "Provide a valid Cloudflare DNS API token through the Compose secret or CLOUDFLARE_API_TOKEN."
            # The caller reports credential failures separately from other setup errors.
            return 3
            ;;
    esac

    # Pass the token through lego's DNS API token settings.
    unset CLOUDFLARE_API_KEY CLOUDFLARE_API_KEY_FILE CF_API_KEY CF_API_KEY_FILE CLOUDFLARE_API_TOKEN
    unset CF_DNS_API_TOKEN CF_DNS_API_TOKEN_FILE CLOUDFLARE_DNS_API_TOKEN CLOUDFLARE_DNS_API_TOKEN_FILE
    if [ "$use_file" = true ]; then
        export CF_DNS_API_TOKEN_FILE="$secret"
    else
        export CF_DNS_API_TOKEN="$token"
    fi
    unset token
    export CLOUDFLARE_PROPAGATION_TIMEOUT="$VALIDATED_PROPAGATION"
    export LEGO_DISABLE_CNAME_SUPPORT="$disable_cname_following"
}
