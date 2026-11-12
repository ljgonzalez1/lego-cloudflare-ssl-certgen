# ==============================================================================
# Dockerfile  --  lego-cloudflare-certgen
# ==============================================================================
# Automated SSL/TLS certificate generation via Let's Encrypt using lego
# and the Cloudflare DNS-01 challenge. No inbound ports required.
#
# Build from the repository root
# ------------------------------
#   docker build -t lego-cloudflare-certgen:lego4.34.0-2.0 ./build
#
# Compose workflow
# ----------------
#   docker compose run --rm certgen
# ==============================================================================
FROM debian:bookworm-slim

ARG LEGO_VERSION=v4.34.0
ARG TARGETARCH
ARG TARGETVARIANT
ARG LEGO_ARCH
ARG LEGO_TARBALL
ARG LEGO_URL
ARG LEGO_SHA256

LABEL org.opencontainers.image.title="lego-cloudflare-certgen" \
      org.opencontainers.image.description="SSL/TLS cert generation via lego + Cloudflare DNS-01 challenge" \
      org.opencontainers.image.source="https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen" \
      com.certgen.lego.version="${LEGO_VERSION}"

# ------------------------------------------------------------------------------
# Runtime defaults. Pass the Cloudflare token at runtime as a Compose secret
# or through CLOUDFLARE_API_KEY with docker run.
# ------------------------------------------------------------------------------
ENV TZ=Etc/UTC \
    PRODUCTION=false \
    PROPAGATION_SECONDS=60 \
    DNS_RESOLVERS="1.1.1.1:53,8.8.8.8:53,1.0.0.1:53" \
    ACCEPT_LEGO_TOS=false \
    UID=1000 \
    GID=1000

# ------------------------------------------------------------------------------
# System packages
# ------------------------------------------------------------------------------
ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates \
        gosu \
        python3 \
        python3-yaml \
        tzdata \
        util-linux \
        wget \
 && rm -rf /var/lib/apt/lists/*

# ------------------------------------------------------------------------------
# Install the lego release matching the target architecture and verify its
# checksum. LEGO_ARCH and LEGO_SHA256 allow explicit build-time overrides.
# ------------------------------------------------------------------------------
RUN set -eu; \
    arch="${LEGO_ARCH:-}"; \
    if [ -z "$arch" ]; then \
        case "${TARGETARCH:-$(dpkg --print-architecture)}" in \
            amd64) arch=linux_amd64 ;; \
            arm64) arch=linux_arm64 ;; \
            386|i386) arch=linux_386 ;; \
            arm|armhf|armel) \
                case "${TARGETVARIANT:-v7}" in \
                    v5|v6|v7) arch="linux_arm${TARGETVARIANT:-v7}" ;; \
                    *) echo "Unsupported ARM variant: $TARGETVARIANT" >&2; exit 1 ;; \
                esac ;; \
            *) echo "Unsupported target architecture. Set LEGO_ARCH explicitly." >&2; exit 1 ;; \
        esac; \
    fi; \
    release="https://github.com/go-acme/lego/releases/download/${LEGO_VERSION}"; \
    archive="${LEGO_TARBALL:-lego_${LEGO_VERSION}_${arch}.tar.gz}"; \
    wget -qO /tmp/lego.tar.gz "${LEGO_URL:-${release}/${archive}}"; \
    checksum="${LEGO_SHA256:-}"; \
    if [ -z "$checksum" ]; then \
        wget -qO /tmp/lego-checksums.txt "${release}/lego_${LEGO_VERSION#v}_checksums.txt"; \
        checksum="$(awk -v archive="$archive" '$2 == archive { print $1; exit }' /tmp/lego-checksums.txt)"; \
        if [ -z "$checksum" ]; then \
            echo "No published checksum for $archive. Set LEGO_SHA256 explicitly." >&2; exit 1; \
        fi; \
    fi; \
    printf '%s  /tmp/lego.tar.gz\n' "$checksum" | sha256sum --check --strict; \
    tar -xzf /tmp/lego.tar.gz -C /usr/local/bin lego; \
    rm -f /tmp/lego.tar.gz /tmp/lego-checksums.txt; \
    chmod 755 /usr/local/bin/lego; \
    lego --version

# ------------------------------------------------------------------------------
# Default user and output mount. The entrypoint uses the configured numeric
# UID/GID to run lego and can use a different host identity without usermod.
# ------------------------------------------------------------------------------
RUN groupadd --gid 1000 certgen \
 && useradd --uid 1000 --gid certgen --no-create-home \
        --home-dir /nonexistent --shell /usr/sbin/nologin certgen \
 && mkdir -p /app /ssl-certs \
 && chown certgen:certgen /ssl-certs

COPY entrypoint.sh validate_env.sh run_lego.sh read_domains.py /usr/local/bin/
RUN chmod 755 /usr/local/bin/entrypoint.sh /usr/local/bin/run_lego.sh

VOLUME ["/ssl-certs"]
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
