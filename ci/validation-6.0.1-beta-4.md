# Validation and build fix — v6.0.1-beta-4

This snapshot is based only on the supplied `lego-cloudflare-certgen-v6.0.1-beta-3-inherited-fixed(1).zip`. It addresses the build failure common to the five supplied architecture logs and the local multiarchitecture Buildx log.

## Diagnosis

All six logs reach the `licensed` stage and fail while requesting the repository-root `COPYING` at aports commit `e78f5987166c6ffbe23f6472e2e8f040298fe2da`. The preceding `runtime-check` image self-test completes. Expected failures of invalid CLI invocations and a portable debugger fallback are not the terminating error.

The official package record identifies the commit as `ca-certificates-bundle` / origin `ca-certificates`, version `20261020-r0`, declared license `MPL-2.0 AND MIT`. The exact aports tree has no repository-root `COPYING`. The earlier offline test incorrectly supplied an invented repository-wide license file, so it did not expose this build failure. Pinning an image digest cannot create that missing file.

Primary evidence inspected:

- [Official Alpine package record](https://pkgs.alpinelinux.org/package/v3.24/main/aarch64/ca-certificates-bundle).
- [Exact aports tree](https://github.com/alpinelinux/aports/tree/e78f5987166c6ffbe23f6472e2e8f040298fe2da).
- [Exact ca-certificates recipe](https://github.com/alpinelinux/aports/blob/e78f5987166c6ffbe23f6472e2e8f040298fe2da/main/ca-certificates/APKBUILD).
- [Alpine 3.24.4 release-data recipe](https://github.com/alpinelinux/aports/blob/105b835acd9971b9770fb61f5a1ddcc133c180dc/main/alpine-base/APKBUILD).
- [Supplemental standard MIT terms](https://github.com/spdx/license-list-data/blob/v3.29.0/text/MIT.txt), copied verbatim to `build/licenses/MIT.txt` (Git blob `d817195dad53ec992418c28ffca5fbd1cd86502a`). The template is not an attribution to this project; upstream attribution remains in the exact APKBUILD.

## Scope of the correction

- Stop requiring a nonexistent aports-wide `COPYING`. Keep the exact package recipe, its contributor notices and upstream archive notices.
- Require recipe name, version, revision and declared license to match the installed APK metadata, using the original package commit on every recipe mirror.
- Keep SHA-512 verification of corresponding source. If the upstream archive is unavailable or returns an invalid HTTP 200 response, try the release-specific official Alpine distfiles mirror with the same filename and checksum. Record the actual retrieval URL.
- Recognize Mozilla license notices in CA data headers, including wrapped comment lines without a copyright line, and retain the full supplemental MPL terms. Copyright alone remains insufficient.
- Supply standard MIT terms only for MIT-declared `alpine-base` release metadata, which is generated directly by its recipe. This does not license other packages or bypass missing upstream notices.
- Include attempted URLs, response failures and installed-package identity in relevant diagnostics.
- Extend regression fixtures to cover the absent root file, checksum-preserving mirror fallback, recipe mismatches, missing upstream license terms, payload changes and the narrow scope of the MIT supplement.
- Update application/Docker/Compose defaults, versions.txt, the final CSV row and generated Markdown tables to beta 4. Preserve release history and all application behavior apart from the version identifier.

The Dockerfile stage structure, build flags, dependencies, architectures, CLI modes, certificate issuance, DNS handling, terminal UI, traces and CI publication gates are unchanged. Base-image digest pinning is not introduced as a speculative fix; APK provenance and corresponding-source checksum checks remain mandatory.

## Executed checks

| Check | Result |
| --- | --- |
| Workflow/event routing and hard publication gates | PASS: 19 tests |
| Publisher/platform digests and release identity, using offline fixtures | PASS: 10 tests |
| Docker Hub HTTP checks, using offline fixtures | PASS: 19 tests |
| Version grammar, tag policy and generated tables | PASS: 10 tests |
| Static format/configuration contracts | PASS: 14 tests |
| Total executed automated regression tests | **72 passed** |
| README.md and DOCKERHUB.md version/table checks for beta 4 | PASS: both direct checks |
| Python syntax | PASS: 16 files |
| Bash / POSIX sh syntax | PASS: 13 Bash and 3 POSIX sh scripts |
| Registered format inventory | PASS: YAML/Actions, Dockerfile, JSON, CSV, Markdown targets, environment template, TOML, PNG/SVG and source discovery; workflow shell parsed without unavailable ShellCheck |
| Embedded jq / awk / sed programs | PASS: 14 / 5 / 8 |
| Source license copies and existing asset fingerprints | PASS; pre-existing asset-rights review status is unchanged |
| Runtime/application source comparison against the supplied beta 3 | PASS: certificate, terminal and debugger sources unchanged; main.go changes only its default version |
| Diff whitespace with the existing CSV CRLF convention (`cr-at-eol`) | PASS |

The format inventory recognizes 36 Go files but does not parse, compile, format or vet them. The 72 passes above exclude every Go and Docker test. The new Go regression cases are included for execution by the existing Go CI job.

## Validation limits

This execution environment has no Go compiler, gofmt, Docker/Buildx, QEMU, ShellCheck or Ruff. The executable named `/usr/bin/go` is an unrelated program, not the Go toolchain. Attempts to obtain development tools through the available package routes did not provide them; outbound build-source downloads from this workspace are restricted. GitHub source evidence was inspected through the connected read interface.

Consequently, the new Go regression cases, Go unit/race/fuzz suites, five actual image smoke jobs, actual upstream license collection and manual multiarchitecture Docker build are **not reported as passed here**. No live ACME order, Cloudflare DNS operation, Docker Hub publication or repository write was performed.

The existing CI still runs its Go and image suites. To exercise the original failing path with Docker locally:

```sh
docker buildx build \
  --builder cf-lego-certgen-multiarch \
  --platform linux/arm/v6,linux/arm/v7,linux/arm64,linux/386,linux/amd64 \
  --progress=plain \
  -t ljgonzalez/lego-cloudflare-certgen:v6.0.1-beta-4 \
  ./build
```

This compiles and checks the target images without publishing them. The unchanged per-platform CI smoke script can also be run as `bash ci/run-smoke.sh linux/amd64`, followed by the other supported platforms.
