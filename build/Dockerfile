# ==============================================================================
# Dockerfile  --  lego-cloudflare-certgen
# ==============================================================================
# Automated SSL/TLS certificate generation via Let's Encrypt using the lego
# ACME client with Cloudflare DNS-01 challenge.  No inbound ports required.
#
# Lego version : v4.34.0  (linux_386 — statically-linked Go binary)
# Base image   : debian:bookworm-slim
# ==============================================================================
#
# Build
# -----
#   docker build -t lego-cloudflare-certgen:lego4.34.0-v0.1 .
#
# Run (env-file)
# ---------------
#   docker run --rm \
#     --env-file ./example.env \
#     --env CLOUDFLARE_API_KEY="${CLOUDFLARE_API_KEY}" \
#     --volume "$(pwd)/ssl-certs:/ssl-certs" \
#     --security-opt no-new-privileges:true \
#     --cap-drop ALL \
#     --cap-add CHOWN \
#     --cap-add SETUID \
#     --cap-add SETGID \
#     lego-cloudflare-certgen:lego4.34.0-v0.1
#
# Run (all inline env vars)
# --------------------------
#   docker run --rm \
#     --env CLOUDFLARE_API_KEY="${CLOUDFLARE_API_KEY}" \
#     --env EMAIL='you@example.com' \
#     --env 'DOMAINS=*.example.com,example.com' \
#     --env ACCEPT_LEGO_TOS=true \
#     --volume "$(pwd)/ssl-certs:/ssl-certs" \
#     --security-opt no-new-privileges:true \
#     --cap-drop ALL \
#     --cap-add CHOWN \
#     --cap-add SETUID \
#     --cap-add SETGID \
#     lego-cloudflare-certgen:lego4.34.0-v0.1
#
# Run (docker compose — recommended; token passed as Docker secret)
# ------------------------------------------------------------------
#   docker compose run --rm certgen
#
# Capabilities needed
# --------------------
#   CHOWN    -- entrypoint chowns the output directory to the target UID/GID
#   SETUID   -- gosu calls setuid() to drop from root to certgen
#   SETGID   -- gosu calls setgid() to drop from root to certgen
#   All other capabilities are dropped.
# ==============================================================================

FROM debian:bookworm-slim

# ------------------------------------------------------------------------------
# Build-time arguments
# All values are baked into the image; none are required at docker run time.
# ------------------------------------------------------------------------------
ARG LEGO_VERSION=v4.34.0
ARG LEGO_ARCH=linux_386
ARG LEGO_TARBALL=lego_${LEGO_VERSION}_${LEGO_ARCH}.tar.gz
ARG LEGO_URL=https://github.com/go-acme/lego/releases/download/${LEGO_VERSION}/${LEGO_TARBALL}

# Optional SHA-256 checksum for the lego archive.
# To obtain it, run:
#   wget -qO- https://github.com/go-acme/lego/releases/download/v4.34.0/lego_v4.34.0_linux_386.tar.gz.sha256sum
# Leave empty to skip verification (not recommended for production builds).
ARG LEGO_SHA256=""

# ------------------------------------------------------------------------------
# Image metadata  (OCI standard labels)
# ------------------------------------------------------------------------------
LABEL org.opencontainers.image.title="lego-cloudflare-certgen" \
      org.opencontainers.image.description="SSL/TLS cert generation via lego + Cloudflare DNS-01 challenge" \
      org.opencontainers.image.version="0.1" \
      org.opencontainers.image.url="https://github.com/go-acme/lego" \
      com.certgen.lego.version="${LEGO_VERSION}" \
      com.certgen.lego.arch="${LEGO_ARCH}"

# ------------------------------------------------------------------------------
# Runtime environment defaults
# Every variable here can be overridden at docker run time via --env or
# --env-file.  None of these are used during the image build itself.
#
# NOTE: 'UID' and 'GID' clash with bash's readonly $UID / $GID built-ins.
#       The entrypoint reads them with 'printenv UID' / 'printenv GID' to
#       bypass bash's variable namespace and read the process environment
#       directly.  This is intentional and documented in validate_env.sh.
#
# NOTE: CLOUDFLARE_API_KEY is intentionally left empty here.  It must be
#       supplied at runtime either via --env or via a Docker secret
#       (cloudflare_api_token) when using docker compose.
# ------------------------------------------------------------------------------
ENV TZ=Etc/UTC \
    EMAIL="" \
    PRODUCTION=false \
    DOMAINS="" \
    CLOUDFLARE_API_KEY="" \
    PROPAGATION_SECONDS=60 \
    DNS_RESOLVERS="1.1.1.1:53,8.8.8.8:53,1.0.0.1:53" \
    ACCEPT_LEGO_TOS=false \
    UID=1000 \
    GID=1000

# ------------------------------------------------------------------------------
# System packages
# DEBIAN_FRONTEND is set as ARG (build-only); it is NOT present at runtime.
# ------------------------------------------------------------------------------
ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates \
        gosu \
        tzdata \
        wget \
 && rm -rf /var/lib/apt/lists/*

# ------------------------------------------------------------------------------
# Install lego
#
# linux_386 is a fully statically-linked Go binary; no shared libraries are
# required.  It runs on any x86_64 Linux kernel that has CONFIG_IA32_EMULATION
# enabled (standard on every major distribution).
# For a native amd64 server, change LEGO_ARCH to linux_amd64.
#
# Token length is validated at runtime (30-50 chars, no whitespace);
# this is a basic structural sanity check and not a cryptographic guarantee.
# ------------------------------------------------------------------------------
RUN set -eu \
 && echo ">>> Downloading lego ${LEGO_VERSION} (${LEGO_ARCH}) ..." \
 && wget -qO /tmp/lego.tar.gz "${LEGO_URL}" \
 && if [ -n "${LEGO_SHA256}" ]; then \
        echo ">>> Verifying SHA-256 checksum ..." ; \
        printf '%s  /tmp/lego.tar.gz\n' "${LEGO_SHA256}" | sha256sum --check --strict ; \
    else \
        echo ">>> WARNING: LEGO_SHA256 not set; skipping checksum verification." ; \
    fi \
 && echo ">>> Extracting lego binary ..." \
 && tar -xzf /tmp/lego.tar.gz -C /usr/local/bin lego \
 && rm -f /tmp/lego.tar.gz \
 && chmod 755 /usr/local/bin/lego \
 && echo ">>> Verifying lego binary ..." \
 && lego --version

# ------------------------------------------------------------------------------
# Non-root user for certificate operations
# Default UID/GID 1000:1000 — remapped at runtime by entrypoint.sh when the
# UID/GID env vars differ.
# ------------------------------------------------------------------------------
RUN groupadd --gid 1000 certgen \
 && useradd  --uid 1000 \
             --gid certgen \
             --no-create-home \
             --home-dir /nonexistent \
             --shell /usr/sbin/nologin \
             certgen

# ------------------------------------------------------------------------------
# Output directory  (mounted as a volume at runtime)
# ------------------------------------------------------------------------------
RUN mkdir -p /ssl-certs \
 && chown certgen:certgen /ssl-certs

# ------------------------------------------------------------------------------
# Scripts
# ------------------------------------------------------------------------------
COPY entrypoint.sh    /usr/local/bin/entrypoint.sh
COPY validate_env.sh  /usr/local/bin/validate_env.sh
COPY run_lego.sh      /usr/local/bin/run_lego.sh

RUN chmod 755 \
        /usr/local/bin/entrypoint.sh \
        /usr/local/bin/validate_env.sh \
        /usr/local/bin/run_lego.sh

# ------------------------------------------------------------------------------
# Volume declaration  (informational; the actual mount is passed via docker run)
# ------------------------------------------------------------------------------
VOLUME ["/ssl-certs"]

# ------------------------------------------------------------------------------
# Entrypoint
# Runs as root; validates env, adjusts certgen UID/GID, prepares the output
# directory, then drops privileges via gosu and exec's run_lego.sh.
# ------------------------------------------------------------------------------
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
