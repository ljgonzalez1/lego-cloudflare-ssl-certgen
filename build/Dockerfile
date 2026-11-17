# syntax=docker/dockerfile:1
ARG LEGO_VERSION=v4.34.0

# Fetch on the builder's native platform. Select assets for the target platform.
FROM --platform=$BUILDPLATFORM alpine:3.24.4 AS download
ARG LEGO_VERSION
ARG TARGETARCH
ARG TARGETVARIANT
ARG LEGO_TARBALL
ARG LEGO_URL
ARG LEGO_SHA256
RUN apk add --no-cache curl
COPY download_lego.sh /tmp/download_lego.sh
RUN sh /tmp/download_lego.sh /out

FROM alpine:3.24.4
ARG LEGO_VERSION
ARG TARGETARCH

LABEL org.opencontainers.image.title="lego-cloudflare-certgen" \
      org.opencontainers.image.description="Let's Encrypt certificates via Cloudflare DNS-01" \
      org.opencontainers.image.source="https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen" \
      org.opencontainers.image.version="3.3" \
      org.opencontainers.image.licenses="MIT" \
      com.certgen.lego.version="${LEGO_VERSION}"

ENV TZ=Etc/UTC \
    PRODUCTION=false \
    PROPAGATION_SECONDS=360 \
    DNS_RESOLVERS="1.1.1.1:53,8.8.8.8:53,1.0.0.1:53" \
    ACCEPT_LEGO_TOS=false \
    UID=1000 \
    GID=1000

# Install only runtime dependencies. The downloader and archives stay in the download stage.
RUN apk add --no-cache bash ca-certificates-bundle py3-yaml su-exec tzdata \
 && addgroup -g 1000 certgen \
 && adduser -D -H -u 1000 -G certgen -h /nonexistent -s /sbin/nologin certgen \
 && mkdir -p /app /ssl-certs \
 && chown certgen:certgen /ssl-certs

COPY --from=download /out/lego /usr/local/bin/lego
COPY --from=download /out/LICENSE /usr/share/licenses/lego/LICENSE
COPY --chmod=755 entrypoint.sh validate_env.sh run_lego.sh read_domains.py print_summary.py /usr/local/bin/

# Run the binary and wrapper dependencies on every target platform during the build.
RUN lego_version="$(su-exec 1000:1000 lego --version)" \
 && printf '%s\n' "$lego_version" \
 && test "$lego_version" = "lego version ${LEGO_VERSION#v} linux/${TARGETARCH}" \
 && smoke_config="$(mktemp)" \
 && trap 'rm -f "$smoke_config"' EXIT \
 && printf 'smoke:\n  - ipv4.example.test\n  - ipv6.example.test\n  - alias.example.test\n  - "*.alias.example.test"\n' > "$smoke_config" \
 && chmod 644 "$smoke_config" \
 && groups="$(/usr/local/bin/entrypoint.sh --config "$smoke_config" --list)" \
 && test "$groups" = "smoke: 4 domains" \
 && TZ=America/Santiago /usr/local/bin/entrypoint.sh --config "$smoke_config" --dry-run >/dev/null

VOLUME ["/ssl-certs"]
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
