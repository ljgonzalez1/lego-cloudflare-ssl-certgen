#!/bin/sh
# Prepare the output volume and switch to the configured UID/GID.
set -efu

case "$0" in
    */*) script_dir=${0%/*} ;;
    *) script_dir=. ;;
esac
. "${script_dir}/validate_env.sh"

usage() {
    printf '%s\n' "Usage: certgen [--list | --dry-run | --test | --staging | --production]
               [--only FOLDER]... [--config FILE] [--output DIRECTORY]

  --list          Validate the YAML and list certificate groups
  --dry-run       Show the planned groups, domains and output paths
  --test          Check lego and preview an offline staging run for CI
  --staging       Issue staging certificates without prompting
  --production    Issue live certificates without confirmation
  --only FOLDER   Process only this group (repeatable)
  --config FILE   Read this YAML file (default: /app/domains.yaml)
  --output DIR    Write to this directory (default: /ssl-certs)"
}

is_output_mounted() {
    [ -r /proc/self/mountinfo ] || return 1
    while IFS=' ' read -r mount_id parent_id device root mount_point rest; do
        if [ "$mount_point" = /ssl-certs ]; then
            return 0
        fi
    done < /proc/self/mountinfo
    return 1
}

CERTGEN_ACTION=issue
CERTGEN_CONFIG=/app/domains.yaml
CERTGEN_OUTPUT=/ssl-certs
requested_mode=

# Queue selected groups behind the CLI arguments that remain to be parsed.
remaining=$#
while [ "$remaining" -gt 0 ]; do
    option=$1
    shift
    remaining=$((remaining - 1))
    case "$option" in
        -h|--help) usage; exit 0 ;;
        --list|--dry-run|--test)
            action=${option#--}
            if [ "$CERTGEN_ACTION" != issue ] && [ "$CERTGEN_ACTION" != "$action" ]; then
                log_err "--list, --dry-run and --test are mutually exclusive."
                exit 2
            fi
            CERTGEN_ACTION=$action
            ;;
        --staging|--production)
            mode=${option#--}
            if [ -n "$requested_mode" ] && [ "$requested_mode" != "$mode" ]; then
                log_err "--staging and --production are mutually exclusive."
                exit 2
            fi
            requested_mode=$mode
            ;;
        --only|--config|--output)
            if [ "$remaining" -eq 0 ]; then
                log_err "$option requires a value."
                exit 2
            fi
            value=$1
            case "$value" in
                ''|--*) log_err "$option requires a value."; exit 2 ;;
            esac
            shift
            remaining=$((remaining - 1))
            case "$option" in
                --only) set -- "$@" "$value" ;;
                --config) CERTGEN_CONFIG=$value ;;
                --output) CERTGEN_OUTPUT=$value ;;
            esac
            ;;
        *)
            log_err "Unknown argument: $option"
            usage >&2
            exit 2
            ;;
    esac
done

if [ "$CERTGEN_ACTION" != issue ] && [ -n "$requested_mode" ]; then
    log_err "--staging and --production cannot be combined with --list, --dry-run or --test."
    exit 2
fi

validate_identity
identity=$(certgen-core identity)
current_uid=${identity%:*}
if [ "$current_uid" -ne 0 ] && [ "$identity" != "$CERT_UID:$CERT_GID" ]; then
    log_err "The container user must match the configured UID/GID ($CERT_UID:$CERT_GID)."
    exit 1
fi

issuance_mode=staging
if [ "$CERTGEN_ACTION" = issue ]; then
    if [ -z "$requested_mode" ]; then
        issuance_mode=$(certgen-core issuance-menu) || exit $?
        case "$issuance_mode" in
            staging|production) ;;
            cancel) log_info "Certificate issuance cancelled."; exit 0 ;;
            *) log_err "Invalid response from the issuance menu."; exit 2 ;;
        esac
    else
        issuance_mode=$requested_mode
        if [ "$issuance_mode" = production ]; then
            log_warn "Production requested by --production. This run submits live orders without confirmation." >&2
        fi
    fi
fi

export CERTGEN_ACTION CERTGEN_CONFIG CERTGEN_OUTPUT

# A custom output directory must already be writable by the target identity.
if [ "$current_uid" -eq 0 ]; then
    if [ "$CERTGEN_ACTION" = issue ] && [ "$CERTGEN_OUTPUT" = /ssl-certs ]; then
        if ! is_output_mounted; then
            log_warn "Mount a host directory at /ssl-certs to keep the output."
        fi
        certgen-core own-output /ssl-certs "$CERT_UID" "$CERT_GID"
    fi
    exec su-exec "$CERT_UID:$CERT_GID" "${script_dir}/run_lego.sh" "$issuance_mode" "$@"
fi

exec "${script_dir}/run_lego.sh" "$issuance_mode" "$@"
