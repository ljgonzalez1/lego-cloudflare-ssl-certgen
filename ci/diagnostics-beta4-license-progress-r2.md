# Intermediate diagnostic package: beta4-license-progress-r2

The application version remains **v6.0.1-beta-4**. This package is a diagnostic revision of the previous beta 4, not a new release or a claim that the complete image build has passed.

## What the supplied log proves

The last provided lines start `certgen-build licenses /runtime /licenses` for five target filesystems. There is no terminating error, elapsed stall duration or successful completion of that stage in the fragment. All five preceding `runtime-check` steps finish with DONE. The missing-terminal and incompatible-option messages are deliberate negative CLI checks. The Delve failures shown on emulated targets trigger the existing portable diagnostic fallback and do not fail those self-tests.

The previous beta 4 has no progress output inside source collection. It resolves and inspects modules sequentially, using `go mod download -json` with a four-minute context per uncached module. HTTP source requests have a three-minute client timeout. The native `licensed` stage is repeated for each target architecture, and its module caches were private to each build container. Consequently, slow source requests, cache locks and CPU work are indistinguishable in the original output. The fragment alone does not identify which operation is slow or prove a deadlock.

## Changes in this revision

- Add architecture-prefixed build-time progress: retained binary, dependency index, Go module download, ZIP h1 verification, notice extraction/scanning, runtime data package identity, HTTP URL/status/body completion, source SHA-512 checks, file hashing and final inventory verification.
- Emit a heartbeat every 15 seconds during quiet work, reporting the last event and its age. A heartbeat is a liveness diagnostic, not a successful check or byte-transfer counter.
- Include Go's JSON error or captured stderr when a module download fails. Redact URL userinfo and non-provenance query strings from the added diagnostics, and normalize control characters.
- Set Cmd.WaitDelay to five seconds. The existing four-minute Go context alone does not bound reads from output pipes inherited by surviving VCS children. This closes a potential additional wait; it is not evidence that this happened in the supplied build.
- Share the native Go module download cache between target architectures with a BuildKit cache mount. Module ZIP h1 verification still runs before collecting notices. No extracted source cache or unchecked notice cache is reused.
- Prefer the pinned GitHub raw aports mirror, retaining the same-commit Alpine fallbacks. Prefer the release-specific Alpine distfiles archive mirror, retaining the original recipe URL as fallback. Every archive candidate must still match the exact recipe SHA-512.
- Add `ci/trace-licenses.sh` and offline tests for its arguments, log capture and exit-status handling; integrate those tests into the existing offline CI job.

No certificate issuance, DNS logic, CLI flags, terminal rendering, debug supervisor, project/lego/Alpine version defaults, tag tables or release publication gates are changed. No license requirement is skipped. The earlier beta-4 provenance and MPL-header corrections remain present.

Primary references for the implementation details:

- [Go subprocess cancellation and WaitDelay](https://pkg.go.dev/os/exec#Cmd).
- [Go module-cache concurrency](https://go.dev/ref/mod#module-cache).
- [Docker cache mounts](https://docs.docker.com/reference/dockerfile/#run---mounttypecache).
- [Docker stage-specific cache bypass](https://docs.docker.com/reference/cli/docker/buildx/build/#no-cache-filter).
- [Docker cache-only output](https://docs.docker.com/build/exporters/#cache-only-export).

## Run it

Extract this ZIP into a new directory and work from its project root. Run:

```sh
bash ci/trace-licenses.sh linux/amd64
```

The first collector line should contain `diagnostic revision=beta4-license-progress-r2`. The default log is `/tmp/certgen-beta4-license-linux-amd64.log`, unless TMPDIR is set. The script prints the exact log path and Buildx exit status. If sudo is required for your Docker setup, use `sudo bash ci/trace-licenses.sh linux/amd64`; the diagnostic code does not require root outside Docker itself.

The equivalent direct build is:

```sh
docker buildx build \
  --builder cf-lego-certgen-multiarch \
  --platform linux/amd64 \
  --target licensed \
  --no-cache-filter licensed \
  --progress=plain \
  --output=type=cacheonly \
  ./build
```

After the native license stage succeeds, run:

```sh
bash ci/trace-licenses.sh all
```

This saves the combined five-target log to `/tmp/certgen-beta4-license-all.log` by default. It exercises the real runtime checks, source collection and strict license verification for each target. It stops at the `licensed` target and does not assemble or publish the final scratch image. The normal full build command remains valid afterwards.

If an operation remains slow, provide the complete saved log, including its last heartbeat or terminating error. A last event mentioning `Go module download`, `HTTP GET`, `HTTP status`, `authenticate Go ZIP`, `scan notices` or `verify generated inventory` distinguishes the main waiting paths. Retrying the original command with no progress instrumentation cannot make that distinction.

## Validation in this environment

Executed results:

| Check | Result |
| --- | --- |
| Diagnostic runner, stdout/stderr capture and pipeline exit codes (fake Docker only) | PASS: 6 tests |
| CI routing and publication gates | PASS: 19 tests |
| Buildx metadata and release identity (offline fixtures) | PASS: 10 tests |
| Docker Hub HTTP behavior (offline fixtures) | PASS: 19 tests |
| Version and tag policy | PASS: 10 tests |
| Static configuration/format contracts | PASS: 14 tests |
| Total automated regression cases executed here | **78 passed** |
| README.md and DOCKERHUB.md beta-4 identity/table checks | PASS |
| Python syntax | PASS: 17 files |
| Shell syntax | PASS: 18 Bash/POSIX scripts |
| Format inventory, including Dockerfile and YAML/workflow shell parsing | PASS, without unavailable ShellCheck |
| Original application and debugger source comparison against the previous beta 4 | PASS: unchanged |
| Project/lego/Alpine versions, CSV, Docker Hub text, Compose and vendored dependencies | PASS: unchanged |
| Diff whitespace check with the inherited CSV CRLF convention | PASS |

These 78 cases do not execute the Go compiler, the new Go tests, Docker, Buildx, upstream downloads or a real image audit. The fake Docker tests validate only the diagnostic runner. The format inventory recognizes Go source files but does not parse or vet them.

 Go unit/race tests, gofmt/vet, real Docker/Buildx execution and live upstream source collection cannot be run here because the required development tools are unavailable. The provided Go regression tests cover heartbeat behavior, credential redaction, download error details, inherited-pipe wait limits, pinned mirror fallback and checksum rejection; they are pending execution in the existing Go CI job.
