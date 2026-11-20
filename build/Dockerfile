# syntax=docker/dockerfile:1.29.0
ARG ALPINE_VERSION=3.24.4
ARG GO_VERSION=1.27.4
ARG APP_VERSION=6.0.1
ARG LEGO_VERSION=v4.34.0

# Every compiler and download dependency remains in native build stages.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine3.24 AS go-toolchain
FROM --platform=$BUILDPLATFORM alpine:${ALPINE_VERSION} AS tools
COPY --from=go-toolchain /usr/local/go/ /usr/local/go/
ENV PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=local GOWORK=off CGO_ENABLED=0
RUN ["apk", "add", "--no-cache", "ca-certificates-bundle"]
WORKDIR /src
COPY go.mod ./
COPY vendor/ ./vendor/
COPY internal/ ./internal/
COPY spinner/ ./spinner/
COPY cmd/ ./cmd/
COPY licenses/ /licenses/
RUN ["go", "build", "-buildvcs=false", "-mod=vendor", "-trimpath", "-tags=netgo,osusergo", "-ldflags=-s -w -buildid=", "-o", "/certgen-build", "./cmd/certgen-build"]
COPY smoke.domains.yaml /smoke.domains.yaml

# Preserve upstream lego and verify its original archive before extracting it.
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
RUN ["/certgen-build", "prepare", "/runtime", "/licenses"]
RUN ["/certgen-build", "compile", "/runtime/usr/local/bin/certgen"]
RUN ["/certgen-build", "verify", "/runtime/usr/local/bin/certgen"]

FROM scratch AS runtime-check
ARG APP_VERSION
ARG LEGO_VERSION
ENV PATH=/usr/local/bin TZ=Etc/UTC ZONEINFO=/usr/share/zoneinfo.zip \
    UID=1000 GID=1000 GOGC=100 GOMEMLIMIT=128MiB \
    PROPAGATION_SECONDS=360 DNS_RESOLVERS="1.1.1.1:53,8.8.8.8:53,1.0.0.1:53" ACCEPT_LEGO_TOS=false
COPY --from=builder /runtime/ /
COPY --from=downloader /out/lego /usr/local/bin/lego
COPY --from=downloader /out/LICENSE /usr/share/licenses/lego/LICENSE
WORKDIR /app
# Mount the test input temporarily: it is not part of the checked filesystem.
RUN --network=none --mount=type=tmpfs,target=/tmp --mount=type=bind,from=tools,source=/smoke.domains.yaml,target=/tmp/domains.yaml,ro ["/usr/local/bin/certgen", "--config", "/tmp/domains.yaml", "--test"]
RUN --network=none ["/usr/local/bin/certgen", "--version"]

# One merged filesystem layer: no tools, sources, test fixtures or debugger bytes.
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
LABEL org.opencontainers.image.title="lego-cloudflare-certgen" \
      org.opencontainers.image.description="Let's Encrypt certificates via Cloudflare DNS-01; optimized static Go runtime" \
      org.opencontainers.image.source="https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.licenses="MIT AND LicenseRef-RuntimeInventory" \
      com.certgen.license.inventory="/usr/share/licenses/manifest.json" \
      com.certgen.alpine.version="${ALPINE_VERSION}" \
      com.certgen.lego.version="${LEGO_VERSION}" \
      com.certgen.go.version="${GO_VERSION}"
ENV PATH=/usr/local/bin TZ=Etc/UTC ZONEINFO=/usr/share/zoneinfo.zip \
    UID=1000 GID=1000 GOGC=100 GOMEMLIMIT=128MiB \
    PROPAGATION_SECONDS=360 DNS_RESOLVERS="1.1.1.1:53,8.8.8.8:53,1.0.0.1:53" ACCEPT_LEGO_TOS=false
COPY --from=licensed /runtime/ /
WORKDIR /app
VOLUME ["/ssl-certs"]
ENTRYPOINT ["/usr/local/bin/certgen"]
