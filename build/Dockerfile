# syntax=docker/dockerfile:1.29.0
ARG ALPINE_VERSION=3.24.4
ARG GO_VERSION=1.27.4
ARG APP_VERSION=6.0.1-beta-2
ARG LEGO_VERSION=v4.34.0

# Build tools always run natively. Go cross-compiles the static target binary.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine3.24 AS go-toolchain
FROM --platform=$BUILDPLATFORM alpine:${ALPINE_VERSION} AS tools
COPY --from=go-toolchain /usr/local/go/ /usr/local/go/
ENV PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=local GOWORK=off CGO_ENABLED=0
RUN ["apk", "add", "--no-cache", "ca-certificates-bundle", "tzdata"]
WORKDIR /src
COPY go.mod ./
COPY vendor/ ./vendor/
COPY internal/ ./internal/
COPY cmd/ ./cmd/
COPY licenses/ /licenses/
RUN ["go", "build", "-buildvcs=false", "-mod=vendor", "-gcflags=all=-N -l -d=checkptr=2", "-ldflags=-compressdwarf=false", "-o", "/certgen-build", "./cmd/certgen-build"]

# Download and SHA-256 verify the official lego binary for the requested CPU.
FROM tools AS downloader
ARG LEGO_VERSION
ARG TARGETARCH
ARG TARGETVARIANT
ARG LEGO_TARBALL
ARG LEGO_URL
ARG LEGO_SHA256
RUN ["/certgen-build", "download", "/out"]

FROM tools AS builder
ARG APP_VERSION
ARG LEGO_VERSION
ARG TARGETARCH
ARG TARGETVARIANT
ARG DELVE_VERSION=v1.27.2
RUN ["/certgen-build", "prepare", "/runtime", "/licenses"]
RUN ["/certgen-build", "compile", "/runtime/usr/local/bin/certgen"]
RUN ["/certgen-build", "verify-debug", "/runtime/usr/local/bin/certgen"]
# Delve supports amd64, arm64 and 386; 32-bit ARM retains full DWARF and traces.
RUN ["/certgen-build", "delve", "/runtime"]

FROM scratch AS runtime-check
ARG APP_VERSION
ARG ALPINE_VERSION
ARG LEGO_VERSION
ARG GO_VERSION
ARG TARGETARCH
ARG TARGETVARIANT

LABEL org.opencontainers.image.title="lego-cloudflare-certgen" \
      org.opencontainers.image.description="Let's Encrypt certificates via Cloudflare DNS-01; Go runtime with pulse progress" \
      org.opencontainers.image.source="https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.licenses="MIT AND LicenseRef-RuntimeInventory" \
      com.certgen.license.inventory="/usr/share/licenses/manifest.json" \
      com.certgen.alpine.version="${ALPINE_VERSION}" \
      com.certgen.lego.version="${LEGO_VERSION}" \
      com.certgen.go.version="${GO_VERSION}"

ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    TZ=Etc/UTC \
    PROPAGATION_SECONDS=360 \
    DNS_RESOLVERS="1.1.1.1:53,8.8.8.8:53,1.0.0.1:53" \
    ACCEPT_LEGO_TOS=false \
    UID=1000 \
    GID=1000 \
    GOGC=off \
    GOMEMLIMIT=256MiB \
    GOTRACEBACK=system \
    CERTGEN_TRACE=errors \
    CERTGEN_LOG_DIR=/ssl-certs/debug

COPY --from=builder /runtime/ /
COPY --from=downloader /out/lego /usr/local/bin/lego
COPY --from=downloader /out/LICENSE /usr/share/licenses/lego/LICENSE
# Keep original source paths for Delve and readable source-level stack traces.
COPY go.mod /src/go.mod
COPY internal/ /src/internal/
COPY cmd/ /src/cmd/
COPY vendor/ /src/vendor/
WORKDIR /app

# Exercise the real entry point and lego without a shell or network.
RUN --network=none --mount=type=tmpfs,target=/tmp ["/usr/local/bin/certgen", "image-self-test"]

# Copy the validated merged filesystem into a fresh scratch image. A debugger
# removed by the real execution probe is absent from every final image layer.
# Inspect the retained payload after debugger probes, then collect only its notices.
FROM tools AS licensed
ARG APP_VERSION
ARG LEGO_VERSION
COPY --from=runtime-check / /runtime/
RUN ["/certgen-build", "licenses", "/runtime", "/licenses"]
RUN --network=none ["/certgen-build", "verify-licenses", "/runtime"]

FROM scratch AS target
ARG APP_VERSION
ARG ALPINE_VERSION
ARG LEGO_VERSION
ARG GO_VERSION
ARG TARGETARCH
ARG TARGETVARIANT

LABEL org.opencontainers.image.title="lego-cloudflare-certgen" \
      org.opencontainers.image.description="Let's Encrypt certificates via Cloudflare DNS-01; Go runtime with pulse progress" \
      org.opencontainers.image.source="https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.licenses="MIT AND LicenseRef-RuntimeInventory" \
      com.certgen.license.inventory="/usr/share/licenses/manifest.json" \
      com.certgen.alpine.version="${ALPINE_VERSION}" \
      com.certgen.lego.version="${LEGO_VERSION}" \
      com.certgen.go.version="${GO_VERSION}"

ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    TZ=Etc/UTC \
    PROPAGATION_SECONDS=360 \
    DNS_RESOLVERS="1.1.1.1:53,8.8.8.8:53,1.0.0.1:53" \
    ACCEPT_LEGO_TOS=false \
    UID=1000 \
    GID=1000 \
    GOGC=off \
    GOMEMLIMIT=256MiB \
    GOTRACEBACK=system \
    CERTGEN_TRACE=errors \
    CERTGEN_LOG_DIR=/ssl-certs/debug

COPY --from=licensed /runtime/ /
WORKDIR /app

VOLUME ["/ssl-certs"]
ENTRYPOINT ["/usr/local/bin/certgen"]
