# Complete build trace: beta4-build-trace-r3

This intermediate diagnostic package retains APP_VERSION=6.0.1-beta-4 and the previous r2 license corrections. It adds tracing; it does not certify the previously reported wait as resolved.

## Coverage

- Every certgen-build invocation reports START, target architecture, native Go version, arguments, END, status and elapsed duration. A heartbeat reports the last event every 15 seconds while work is quiet.
- Bootstrap, application and Delve compilation use Go -v -x, printing packages and underlying compiler/linker commands. apk installation uses --verbose. Go's own verbose streams are forwarded as emitted by Go; structured helper diagnostics retain URL credential/query redaction.
- Runtime assembly reports file and directory paths, byte counts, modes and symbolic links, without dumping file contents or the process environment.
- Lego release downloads report attempt counts, HTTP status, expected/received size, retries, verified SHA-256 and extraction start. The outer command reports extraction completion or failure.
- Delve preparation reports its command and dependency notice copies.
- Image self-tests label debugger probes, the offline preview, and each negative CLI test with its expected status. Expected status 2 is explicitly marked PASS after verification.
- License collection retains r2's detailed source, HTTP, checksum and inventory progress, and adds command invocation and cleanup messages.

The existing application arguments, certificate issuance, DNS behavior, dependency versions, release defaults, tag tables and publication gates remain unchanged. Runtime self-test output is more explicit; normal issuance UI and debugger logic are unchanged.

## Run the complete build

From the extracted project root:

```sh
sudo bash ci/trace-build.sh all
```

This runs all five architectures through the final target, uses --progress=plain and --no-cache, saves both output streams, preserves Docker's exit status and uses --output=type=cacheonly. It neither pushes nor loads an image. --no-cache reruns Dockerfile instructions, but does not erase the authenticated Go download cache mount.

The default log is /tmp/certgen-beta4-build-r3-all.log; TMPDIR can change that parent directory. The script prints the actual path. A native-only diagnostic is:

```sh
sudo bash ci/trace-build.sh linux/amd64
```

The builder defaults to cf-lego-certgen-multiarch. CERTGEN_BUILDER overrides it. The equivalent direct invocation is:

```sh
sudo docker buildx build \
  --builder cf-lego-certgen-multiarch \
  --platform linux/arm/v6,linux/arm/v7,linux/arm64,linux/386,linux/amd64 \
  --target target \
  --no-cache \
  --progress=plain \
  --output=type=cacheonly \
  ./build
```

A cached RUN does not execute, so it cannot emit fresh command traces; --no-cache requests those executions. COPY/FROM/export progress is emitted by BuildKit itself. Registry transfers happen before the Go helper is available and are diagnosed using BuildKit output, not the helper heartbeat.

## If BuildKit reports output clipping

Log limits belong to the BuildKit daemon, not to Dockerfile build arguments. Create a separate diagnostic builder once, keeping the existing builder intact:

```sh
sudo docker buildx create \
  --name cf-lego-certgen-trace-r3 \
  --driver docker-container \
  --driver-opt image=moby/buildkit:v0.35.1 \
  --driver-opt env.BUILDKIT_STEP_LOG_MAX_SIZE=104857600 \
  --driver-opt env.BUILDKIT_STEP_LOG_MAX_SPEED=10485760 \
  --bootstrap

sudo env CERTGEN_BUILDER=cf-lego-certgen-trace-r3 bash ci/trace-build.sh all
```

These are finite limits of 100 MiB per step and 10 MiB/s. A new builder begins with its own cache. These settings do not change application runtime behavior.

References: [Buildx progress/cache flags](https://docs.docker.com/reference/cli/docker/buildx/build/), [driver environment options](https://docs.docker.com/build/builders/drivers/docker-container/), [BuildKit log limiter](https://github.com/moby/buildkit/blob/master/util/progress/logs/logs.go).

## Validation

The diagnostic-runner suite passes 8 cases, including two new complete-build cases that check the final target, no-push behavior and preservation of a nonzero Docker status through tee. Static contract and workflow suites pass 14 and 19 cases respectively. Additional available regression checks are recorded after execution. No real Go compiler, gofmt, Docker/Buildx or QEMU is available here, so actual compilation, the Go suites and target-image execution remain unverified locally. No remote publication or ACME order is performed.

Final available regression total: **80 passed** (8 runner, 14 static contracts, 19 workflow, 10 publisher, 19 Docker Hub fixtures, 10 version/tag tests). Both Markdown version checks pass. Go and actual Docker image tests remain pending.
