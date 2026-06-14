# ==============================================================================
# Dockerfile  --  lego-cloudflare-certgen
# ==============================================================================

FROM debian:13.5-slim

ARG LEGO_VERSION=v5.2.1
ARG LEGO_VERSION_NUM=5.2.1

ARG LEGO_ARCH=linux_amd64

ARG LEGO_TARBALL=lego_${LEGO_VERSION}_${LEGO_ARCH}.tar.gz
ARG LEGO_BASE_URL=https://github.com/go-acme/lego/releases/download/${LEGO_VERSION}

ARG LEGO_URL=${LEGO_BASE_URL}/${LEGO_TARBALL}
ARG LEGO_CHECKSUMS_URL=${LEGO_BASE_URL}/${LEGO_CHECKSUMS}

ARG LEGO_CHECKSUMS=lego_${LEGO_VERSION_NUM}_checksums.txt

ARG LEGO_SHA256=""

# ------------------------------------------------------------------------------
# Image metadata  (OCI standard labels)
# ------------------------------------------------------------------------------
LABEL org.opencontainers.image.title="lego-cloudflare-certgen" \
      org.opencontainers.image.description="SSL/TLS cert generation via lego + Cloudflare DNS challenge" \
      org.opencontainers.image.version="0.2" \
      org.opencontainers.image.url="https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen" \
      com.certgen.lego.version="${LEGO_VERSION}" \
      com.certgen.lego.arch="${LEGO_ARCH}"

# ------------------------------------------------------------------------------
# Environment Variables
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
# DEBIAN_FRONTEND is set as ARG (build-only).
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
# Install lego  (with MANDATORY SHA-256 verification)
#
# Verification flow (the build FAILS and stops on any mismatch):
#   1. Download the release tarball.
#   2. Download the official combined checksums file for this exact version.
#   3. Extract the expected hash for the EXACT tarball name (end-anchored, so a
#      '<name>.sbom.json' line cannot match by accident on releases that ship
#      SBOM entries, e.g. v5.2.x).  No entry => version/arch misconfigured.
#   4. Compute the actual sha256sum of the downloaded tarball.
#   5. Compare.  On mismatch: print expected vs actual and exit 1.
#   6. (Optional) If LEGO_SHA256 is pinned, assert it equals the upstream hash.
#
# Only grep / sha256sum (coreutils) / shell builtins are used here - no awk or
# cut dependency, so this works on a bare debian:bookworm-slim.
# ------------------------------------------------------------------------------
RUN set -eu \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      wget \
      tar \
 && rm -rf /var/lib/apt/lists/*
 
RUN set -eu \
 && echo ">>> Downloading lego ${LEGO_VERSION} (${LEGO_ARCH}) ..." \
 && wget -qO /tmp/lego.tar.gz "${LEGO_URL}" \
 && echo ">>> Downloading checksums: ${LEGO_CHECKSUMS_URL} ..." \
 && wget -qO /tmp/lego_checksums.txt "${LEGO_CHECKSUMS_URL}"

 
RUN set -eu \
 && echo ">>> Looking up expected checksum for ${LEGO_TARBALL} ..." \
 && _expected="" \
 && while read -r _sum _name; do \
        if [ "${_name}" = "${LEGO_TARBALL}" ]; then \
            _expected="${_sum}" ; \
            break ; \
        fi ; \
    done < /tmp/lego_checksums.txt \
 && if [ -z "${_expected}" ]; then \
        echo "ERROR: no checksum entry found for '${LEGO_TARBALL}'." >&2 ; \
        echo "       Check LEGO_VERSION / LEGO_ARCH against:" >&2 ; \
        echo "       ${LEGO_CHECKSUMS_URL}" >&2 ; \
        exit 1 ; \
    fi \
 && _actual="$(sha256sum /tmp/lego.tar.gz)" \
 && _actual="${_actual%% *}" \
 && echo ">>> Verifying SHA-256 ..." \
 && if [ "${_expected}" != "${_actual}" ]; then \
        echo "============================================================" >&2 ; \
        echo "ERROR: SHA-256 CHECKSUM MISMATCH - aborting build" >&2 ; \
        echo "  file     : ${LEGO_TARBALL}" >&2 ; \
        echo "  expected : ${_expected}" >&2 ; \
        echo "  actual   : ${_actual}" >&2 ; \
        echo "============================================================" >&2 ; \
        exit 1 ; \
    fi \
 && echo ">>> Checksum OK: ${_actual}" \
 && if [ -n "${LEGO_SHA256}" ] && [ "${LEGO_SHA256}" != "${_expected}" ]; then \
        echo "============================================================" >&2 ; \
        echo "ERROR: pinned LEGO_SHA256 does not match the upstream checksum" >&2 ; \
        echo "  pinned   : ${LEGO_SHA256}" >&2 ; \
        echo "  upstream : ${_expected}" >&2 ; \
        echo "  (a swapped/tampered checksums file would trigger this)" >&2 ; \
        echo "============================================================" >&2 ; \
        exit 1 ; \
    fi

RUN set -eu \
 && echo ">>> Extracting lego binary ..." \
 && mkdir -p /tmp/lego-bin \
 && tar -xzf /tmp/lego.tar.gz -C /tmp/lego-bin lego \
 && chmod 755 /tmp/lego-bin/lego

RUN set -eu \
 && echo ">>> Verifying lego binary ..." \
 && /tmp/lego-bin/lego --version


# ------------------------------------------------------------------------------
# Non-root user for certificate operations
# Default UID/GID 1000:1000 - remapped at runtime by entrypoint.sh when the
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
COPY entrypoint.sh        /usr/local/bin/entrypoint.sh
COPY validate_env.sh      /usr/local/bin/validate_env.sh
COPY validate_domains.sh  /usr/local/bin/validate_domains.sh
COPY run_lego.sh          /usr/local/bin/run_lego.sh

RUN chmod 755 \
        /usr/local/bin/entrypoint.sh \
        /usr/local/bin/validate_env.sh \
        /usr/local/bin/validate_domains.sh \
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
