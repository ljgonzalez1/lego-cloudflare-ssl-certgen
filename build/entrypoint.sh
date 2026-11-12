#!/usr/bin/env bash
# ==============================================================================
# entrypoint.sh  --  Container entry point  (runs as root)
# ==============================================================================
# Prepares the output volume and drops privileges before reading domains.yaml
# or running lego. The target identity defaults to UID/GID 1000:1000.
# ==============================================================================
set -euo pipefail

script_dir="$(dirname -- "${BASH_SOURCE[0]}")"

# shellcheck source=validate_env.sh
. "${script_dir}/validate_env.sh"

usage() {
    cat <<'EOF'
Usage: certgen [--list | --dry-run] [--staging | --production]
               [--only FOLDER]... [--config FILE] [--output DIRECTORY]

  --list          Validate the YAML and list certificate groups
  --dry-run       Show the planned groups, domains and output paths
  --only FOLDER   Process only this group (repeatable)
  --config FILE   Read this YAML file (default: /app/domains.yaml)
  --output DIR    Write to this directory (default: /ssl-certs)
  --staging       Use Let's Encrypt staging
  --production    Use Let's Encrypt production
EOF
}

CERTGEN_ACTION=issue
CERTGEN_CONFIG=/app/domains.yaml
CERTGEN_OUTPUT=/ssl-certs
mode_override=
selected=()

while (( $# )); do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --list|--dry-run)
            action="${1#--}"
            if [[ "${CERTGEN_ACTION}" != issue && "${CERTGEN_ACTION}" != "${action}" ]]; then
                log_err "--list and --dry-run are mutually exclusive."
                exit 2
            fi
            CERTGEN_ACTION="${action}"
            ;;
        --staging|--production)
            mode="${1#--}"
            if [[ -n "${mode_override}" && "${mode_override}" != "${mode}" ]]; then
                log_err "--staging and --production are mutually exclusive."
                exit 2
            fi
            mode_override="${mode}"
            ;;
        --only|--config|--output)
            if [[ -z "${2:-}" || "$2" == --* ]]; then
                log_err "$1 requires a value."
                exit 2
            fi
            case "$1" in
                --only) selected+=("$2") ;;
                --config) CERTGEN_CONFIG="$2" ;;
                --output) CERTGEN_OUTPUT="$2" ;;
            esac
            shift
            ;;
        *)
            log_err "Unknown argument: $1"
            usage >&2
            exit 2
            ;;
    esac
    shift
done

case "${mode_override}" in
    staging) export PRODUCTION=false ;;
    production) export PRODUCTION=true ;;
esac

validate_identity
export CERTGEN_ACTION CERTGEN_CONFIG CERTGEN_OUTPUT

# Root only prepares the standard output mount. A custom --output directory
# must already be writable by the requested UID/GID.
if (( EUID == 0 )); then
    if [[ "${CERTGEN_ACTION}" == issue && "${CERTGEN_OUTPUT}" == /ssl-certs ]]; then
        if ! findmnt -M /ssl-certs >/dev/null 2>&1; then
            log_warn "Mount a host directory at /ssl-certs to keep the output."
        fi
        chown -- "${CERT_UID}:${CERT_GID}" /ssl-certs
    fi

    exec gosu "${CERT_UID}:${CERT_GID}" "${script_dir}/run_lego.sh" "${selected[@]}"
fi

if [[ "${EUID}" != "${CERT_UID}" || "$(id -g)" != "${CERT_GID}" ]]; then
    log_err "The container user must match the configured UID/GID (${CERT_UID}:${CERT_GID})."
    exit 1
fi

exec "${script_dir}/run_lego.sh" "${selected[@]}"
