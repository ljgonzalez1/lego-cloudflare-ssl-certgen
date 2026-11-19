# syntax=docker/dockerfile:1
ARG ALPINE_VERSION=3.24.4
ARG APP_VERSION=5.2.2
ARG LEGO_VERSION=v4.34.0

# Download and verify the official binary on the builder's native architecture.
FROM --platform=$BUILDPLATFORM alpine:${ALPINE_VERSION} AS downloader
ARG LEGO_VERSION
ARG TARGETARCH
ARG TARGETVARIANT
ARG LEGO_TARBALL
ARG LEGO_URL
ARG LEGO_SHA256
RUN apk add --no-cache curl
COPY download_lego.sh /tmp/download_lego.sh
RUN /bin/sh /tmp/download_lego.sh /out

# Compile the C++ program and assemble an Alpine filesystem for the target CPU.
FROM alpine:${ALPINE_VERSION} AS builder
ARG LEGO_VERSION
RUN apk add --no-cache build-base yaml-dev yaml-static dash
COPY src/ /src/
RUN mkdir -p /runtime/usr/local/bin \
 && c++ -std=c++17 -O2 -Wall -Wextra -Wpedantic \
       -ffunction-sections -fdata-sections -Wl,--gc-sections -static -s \
       /src/*.cpp -lyaml -o /runtime/usr/local/bin/certgen-core

# Installing Dash itself through APK pulls in BusyBox for package scripts. Copy
# the shell and install the other runtime packages into an independent APK root.
RUN mkdir -p /runtime/bin /runtime/usr/bin /runtime/etc /runtime/app \
             /runtime/tmp /runtime/ssl-certs /runtime/etc/apk \
 && cp -R /etc/apk/keys /runtime/etc/apk/ \
 && cp /etc/apk/repositories /runtime/etc/apk/ \
 && apk --root /runtime --initdb --no-cache --no-scripts add \
        musl ca-certificates-bundle tzdata su-exec \
 && cp /usr/bin/dash /runtime/usr/bin/dash \
 && ln -s /usr/bin/dash /runtime/bin/sh \
 && cp /etc/alpine-release /runtime/etc/alpine-release \
 && cp -L /etc/os-release /runtime/etc/os-release \
 && printf 'root:x:0:0:root:/root:/bin/sh\ncertgen:x:1000:1000:certgen:/nonexistent:/sbin/nologin\n' > /runtime/etc/passwd \
 && printf 'root:x:0:\ncertgen:x:1000:\n' > /runtime/etc/group \
 && mkdir -p /runtime/root \
 && chmod 1777 /runtime/tmp \
 && chown 1000:1000 /runtime/ssl-certs

# License texts the shipped binaries require, and a NOTICE with exact versions
# and sources. Build-only packages are not shipped, so they need no texts.
COPY licenses/ /licenses/
COPY write_notices.sh /tmp/write_notices.sh
RUN /bin/sh /tmp/write_notices.sh /licenses /runtime "$LEGO_VERSION"

# The filesystem above is Alpine. Starting with scratch keeps the builder's
# layers and their BusyBox, curl, headers and compiler out of the resulting image.
FROM scratch AS target
ARG APP_VERSION
ARG ALPINE_VERSION
ARG LEGO_VERSION
ARG TARGETARCH
ARG TARGETVARIANT

LABEL org.opencontainers.image.title="lego-cloudflare-certgen" \
      org.opencontainers.image.description="Let's Encrypt certificates via Cloudflare DNS-01" \
      org.opencontainers.image.source="https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.licenses="MIT" \
      com.certgen.alpine.version="${ALPINE_VERSION}" \
      com.certgen.lego.version="${LEGO_VERSION}"

ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    TZ=Etc/UTC \
    PROPAGATION_SECONDS=360 \
    DNS_RESOLVERS="1.1.1.1:53,8.8.8.8:53,1.0.0.1:53" \
    ACCEPT_LEGO_TOS=false \
    UID=1000 \
    GID=1000

COPY --from=builder /runtime/ /
COPY --from=downloader /out/lego /usr/local/bin/lego
COPY --from=downloader /out/LICENSE /usr/share/licenses/lego/LICENSE
COPY --chmod=755 entrypoint.sh validate_env.sh run_lego.sh /usr/local/bin/

WORKDIR /app

# Fail the build if the shell or either executable cannot run on this platform.
RUN --network=none --mount=type=tmpfs,target=/tmp set -eu \
 && test -L /bin/sh && test /bin/sh -ef /usr/bin/dash \
 && for tool in busybox ash bash python python3 cpp c++ curl apk; do \
        if command -v "$tool" >/dev/null; then printf 'Unexpected runtime program: %s\n' "$tool" >&2; exit 1; fi; \
    done \
 && test ! -e /bin/busybox && test ! -e /bin/ash \
 && for license in NOTICE lego/LICENSE musl/COPYRIGHT dash/COPYING dash/GPL-2.0-or-later \
        su-exec/LICENSE ca-certificates/MPL-2.0 libyaml/LICENSE; do \
        test -s "/usr/share/licenses/$license"; \
    done \
 && lego_version="$(su-exec 1000:1000 lego --version)" \
 && printf '%s (%s/%s)\n' "$lego_version" "$TARGETARCH" "$TARGETVARIANT" \
 && test "$lego_version" = "lego version ${LEGO_VERSION#v} linux/${TARGETARCH}" \
 && test "$(su-exec 1000:1000 certgen-core identity)" = '1000:1000' \
 && smoke_config=/tmp/smoke-domains.yaml \
 && umask 022 \
 && printf 'smoke:\n  - ipv4.example.test\n  - ipv6.example.test\n  - alias.example.test\n  - "*.alias.example.test"\n  - planned.example.test\n' > "$smoke_config" \
 && plan="$(TZ=America/Santiago entrypoint.sh --config "$smoke_config" --output /tmp/no-orders --test)" \
 && case "$plan" in *'Offline test passed.'*) ;; *) printf '%s\n' "$plan" >&2; exit 1 ;; esac \
 && test ! -e /tmp/no-orders \
 && expect_rejection() { \
        if message=$("$@" </dev/null 2>&1); then \
            printf 'Unexpected success: %s\n' "$*" >&2; return 1; \
        else \
            status=$?; \
            if [ "$status" -ne 2 ]; then printf '%s\n' "$message" >&2; return 1; fi; \
        fi; \
    } \
 && expect_rejection entrypoint.sh --config "$smoke_config" \
 && expect_rejection entrypoint.sh --config "$smoke_config" --test --production \
 && printf 'bad: ["not a DNS name"]\n' > /tmp/invalid-domains.yaml \
 && expect_rejection entrypoint.sh --config /tmp/invalid-domains.yaml --test \
 && printf 'smoke\t5\t0\n' | certgen-core summary >/dev/null

VOLUME ["/ssl-certs"]
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
