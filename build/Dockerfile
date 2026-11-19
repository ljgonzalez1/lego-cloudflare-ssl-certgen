# syntax=docker/dockerfile:1
ARG ALPINE_VERSION=3.24.4
ARG APP_VERSION=5.2.4
ARG LEGO_VERSION=v4.34.0

# Download and verify the official binary on the builder's native architecture.
FROM --platform=$BUILDPLATFORM alpine:${ALPINE_VERSION} AS downloader
ARG LEGO_VERSION
ARG TARGETARCH
ARG TARGETVARIANT
ARG LEGO_TARBALL
ARG LEGO_URL
ARG LEGO_SHA256
RUN ["apk", "add", "--no-cache", "build-base", "curl"]
COPY tools.cpp /src/tools.cpp
RUN ["g++", "-std=c++20", "-Og", "-g3", "-Wall", "-Wextra", "/src/tools.cpp", "-o", "/certgen-build"]
RUN ["/certgen-build", "download", "/out"]

# Compile and retain debug information on every target architecture.
FROM alpine:${ALPINE_VERSION} AS builder
ARG LEGO_VERSION
RUN ["apk", "add", "--no-cache", "build-base", "yaml-dev", "yaml-static"]
COPY tools.cpp /src/tools.cpp
COPY licenses/ /licenses/
RUN ["g++", "-std=c++20", "-Og", "-g3", "-Wall", "-Wextra", "/src/tools.cpp", "-o", "/certgen-build"]
RUN ["/certgen-build", "prepare", "/runtime", "/licenses"]
COPY src/ /src/
RUN ["g++", "-std=c++20", "-Og", "-g3", "-Wall", "-Wextra", "-static", "/src/main.cpp", "/src/app.cpp", "/src/settings.cpp", "/src/child.cpp", "/src/image_test.cpp", "/src/domains.cpp", "/src/runtime.cpp", "/src/menu.cpp", "/src/summary.cpp", "/src/supervise.cpp", "/src/lego_log.cpp", "/src/lego_view.cpp", "-lyaml", "-o", "/runtime/usr/local/bin/certgen"]

RUN ["/certgen-build", "verify-debug", "/runtime/usr/local/bin/certgen"]

# Copy only the application, lego and runtime data. The build tools stay behind.
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
WORKDIR /app

# Exercise the real entry point and lego without a shell or network.
RUN --network=none --mount=type=tmpfs,target=/tmp ["/usr/local/bin/certgen", "image-self-test"]

VOLUME ["/ssl-certs"]
ENTRYPOINT ["/usr/local/bin/certgen"]
