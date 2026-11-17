# :closed_lock_with_key: lego-cloudflare-certgen

> One-shot Docker container for automated SSL/TLS certificate generation via **Let's Encrypt** using [lego](https://github.com/go-acme/lego) v4.34.0 and **Cloudflare DNS-01 challenge**. No HTTP ports or web server required.

---

![Project banner](assets/readme-banner.png)

---

## :sparkles: Features

The container wraps lego with YAML configuration, Compose secrets and a certificate report.

1. **Keep your settings in `.env` and your domains in YAML.** Each top-level key in `domains.yaml` represents one certificate and its output folder. Its list contains all the DNS names on that certificate.

2. **Use Docker Compose for a repeatable, secrets-safe workflow.** Docker Compose provides the Cloudflare API token as a [Docker secret](https://docs.docker.com/compose/use-secrets/). lego reads the secret file directly.

3. **See the outcome of each certificate request.** The final table lists attempted, issued, failed and skipped DNS names for every selected group, with totals for names and certificates.

---

## :whale: Requirements

- Docker Engine and the Docker Compose plugin if using Compose
- A Cloudflare [**DNS API token**](https://developers.cloudflare.com/fundamentals/api/get-started/create-token/) with `Zone > DNS > Edit` and `Zone > Zone > Read` for the relevant zones

---

## 🚀 Usage

Copy `example.env` to `.env` or update an existing file. Fill in `EMAIL`, `CLOUDFLARE_API_TOKEN` and `ACCEPT_LEGO_TOS=true`. Use `domains.yaml` for your names and create the output directory.

```bash
test -e .env || (umask 077; cp example.env .env)
chmod 600 .env
mkdir -p ssl-certs
```

### Docker run

```bash
docker run --rm -t --env-file .env \
  -v ./domains.yaml:/app/domains.yaml:ro -v ./ssl-certs:/ssl-certs \
  ljgonzalez/lego-cloudflare-certgen:4.0.0
```

Docker Engine 23 and later accept relative bind paths. On older versions, use `"$PWD/domains.yaml:/app/domains.yaml:ro"` and `"$PWD/ssl-certs:/ssl-certs"`. The default Docker capabilities include CHOWN, SETUID and SETGID. `-t` enables ANSI output. [Docker run](https://docs.docker.com/reference/cli/docker/container/run/#volume) · [Default capabilities](https://docs.docker.com/engine/containers/run/#runtime-privilege-and-linux-capabilities)

### Docker Compose

```bash
docker compose run --rm certgen
```

Compose forwards the token as a secret file and reads the other settings from `.env`. If you previously kept settings in `certgen.env`, keep the token in `.env` and run `CERTGEN_ENV_FILE=./certgen.env docker compose run --rm certgen`. Set `CERTGEN_IMAGE` to use another locally built tag.

### Preview and issue

```bash
DOMAINS_FILE=./template.domains.yaml docker compose run --rm certgen --list
DOMAINS_FILE=./template.domains.yaml docker compose run --rm certgen --dry-run --only certificateA
docker compose run --rm certgen --production
```

Use the first two commands to preview a configuration. The last command requests production certificates. A run without `--production` uses staging by default.

Certificates appear in `./ssl-certs/<group>/<timestamp>/requested_<timestamp>_expires_<timestamp>/`. For example:

```text
ssl-certs/
└── certificateA/
    └── 2026-11-11_17.18.00_GMT-3/
        └── requested_2026-11-11_17.18.55_GMT-3_expires_2027-02-09_05.18.00_GMT-3/
            ├── accounts/
            │   └── acme-v02.api.letsencrypt.org/
            │       └── you@example.com/
            └── certificates/
                ├── example.com.crt
                ├── example.com.issuer.crt
                ├── example.com.json
                └── example.com.key
```

The directory timestamps use the original script’s naming convention and offsets. Read the exact expiry from the issued certificate with `openssl x509 -in example.com.crt -noout -enddate`.

---

## 🛠️ Build

Version **4.0.0** uses **Alpine 3.24.4** and the official **lego v4.34.0** binaries. Run the build commands from the directory that contains this README.

| Target | Buildx platform | Official lego archive |
|---|---|---|
| AMD64 / x86-64 | `linux/amd64` | `lego_v4.34.0_linux_amd64.tar.gz` |
| i386 / 32-bit x86 | `linux/386` | `lego_v4.34.0_linux_386.tar.gz` |
| ARMv7 / Debian armhf | `linux/arm/v7` | `lego_v4.34.0_linux_armv7.tar.gz` |
| ARMv8 / AArch64 | `linux/arm64` | `lego_v4.34.0_linux_arm64.tar.gz` |

The ARMv7 image uses the Alpine `armv7` userspace. Debian calls that architecture `armhf`. [Alpine image platforms](https://hub.docker.com/_/alpine) · [lego v4.34.0](https://github.com/go-acme/lego/releases/tag/v4.34.0)

### Set up Buildx

Use a running Docker Engine, Buildx and QEMU registered on the Docker host for non-native targets. The builder also needs network access to Docker Hub, GitHub Releases and the base images' package mirrors.

```bash
docker buildx ls
# Create this builder once. If it exists, use docker buildx use lego-multiarch.
docker buildx create --name lego-multiarch --driver docker-container --use
docker buildx inspect lego-multiarch --bootstrap
```

Check the builder's `Platforms` line for all four targets. On Linux, if any are missing, register the corresponding emulators and inspect again. [Docker multi-platform builds](https://docs.docker.com/build/building/multi-platform/)

```bash
docker run --privileged --rm tonistiigi/binfmt --install arm64,arm,386
docker buildx inspect lego-multiarch --bootstrap
```

### Build locally

Use your target platform in place of `linux/amd64`. `--load` loads this single-platform image into the local Engine for testing.

```bash
docker buildx build --builder lego-multiarch --platform linux/amd64 \
  --progress=plain --load -t ljgonzalez/lego-cloudflare-certgen:4.0.0 ./build

docker run --rm --entrypoint lego ljgonzalez/lego-cloudflare-certgen:4.0.0 --version
docker run --rm -v "$PWD/template.domains.yaml:/app/domains.yaml:ro" \
  ljgonzalez/lego-cloudflare-certgen:4.0.0 --list
```

To build and load each architecture locally with a distinct tag, use this loop.

```sh
for arch in amd64 386 arm/v7 arm64; do
  suffix=$(printf '%s' "$arch" | tr / -)
  docker buildx build --builder lego-multiarch --platform "linux/$arch" \
    --progress=plain --load -t "ljgonzalez/lego-cloudflare-certgen:4.0.0-$suffix" ./build || break
done
```

### 🪶 Runtime image

The Alpine downloader selects the official lego archive for the target CPU and verifies its SHA-256 checksum. The Alpine builder compiles `certgen-core` with C++17 and statically links libyaml. The final stage starts from `scratch` and receives a minimal Alpine 3.24 filesystem with musl, the CA bundle, timezone data and `su-exec`. Dash provides `/bin/sh`. The container runs the POSIX scripts, `certgen-core` and lego. BusyBox, Ash, Bash, Python, the compiler and the downloader stay out of the runtime.

Each platform build runs the installed lego binary as the non-root user, checks its version and architecture, then exercises `--list`, `--dry-run` and the report. These checks run locally inside the build without requesting a certificate.

Inspect the loaded image size in MiB.

```sh
docker image inspect --format '{{.Size}}' ljgonzalez/lego-cloudflare-certgen:4.0.0 |
  awk '{printf "%.1f MiB\n", $1 / 1048576}'
```

---

## :gear: Configuration reference

All variables are optional unless marked **required**.

| Variable | Default | Required | Description |
|---|---|---|---|
| `TZ` | `Etc/UTC` | :x: | IANA timezone for directory names. Use `America/Santiago` to match the original script. |
| `EMAIL` | - | :heavy_check_mark: | Let's Encrypt account contact e-mail for issuance |
| `PRODUCTION` | `false` | :x: | Set `true` to issue browser-trusted certificates |
| `CLOUDFLARE_API_TOKEN` | - | :heavy_check_mark: | Cloudflare DNS API token for Docker run and the Compose secret. Docker run also accepts the legacy `CLOUDFLARE_API_KEY` variable. |
| `PROPAGATION_SECONDS` | `360` | :x: | Maximum wait for TXT propagation per challenge, in seconds |
| `DNS_RESOLVERS` | `1.1.1.1:53,8.8.8.8:53,1.0.0.1:53` | :x: | Comma-separated resolvers used to verify TXT record visibility |
| `ACCEPT_LEGO_TOS` | `false` | :heavy_check_mark: | Set `true` after reading the [Let's Encrypt Terms of Service](https://letsencrypt.org/repository/) |
| `UID` | `1000` | :x: | UID used to write certificates and ACME account data |
| `GID` | `1000` | :x: | GID used to write certificates and ACME account data |
| `DOMAINS_FILE` | `./domains.yaml` | :x: | Host YAML path for Compose |
| `CERTGEN_IMAGE` | `ljgonzalez/lego-cloudflare-certgen:4.0.0` | :x: | Image used by Compose |
| `CERTGEN_ENV_FILE` | `./.env` | :x: | Settings file read by the Compose service |

### Certificate groups and wildcard domains

If you need a new `domains.yaml`, copy [`template.domains.yaml`](template.domains.yaml) and replace the sample DNS names.

```yaml
certificateA:
  - example.com
  - www.example.com
  - "*.example.com"

certificateB:
  - example.net
  - "*.example.net"
```

- Quote wildcard names so YAML reads them as strings.
- Use letters, numbers, hyphens or underscores for the folder name, starting with a letter or number.
- List up to 100 distinct DNS names per certificate. A domain can appear in multiple groups.
- The first DNS name determines lego’s certificate filename.

### Hostnames and DNS-01

List the DNS names you want in each certificate group, including any quoted wildcards. The same request path works for names with A, AAAA or CNAME service records and for planned names awaiting a service record. lego uses the Cloudflare token to find the managed zone, write a temporary `_acme-challenge` TXT record and clean it up. Let's Encrypt validates the TXT record before issuing. [Let's Encrypt DNS-01 documentation](https://letsencrypt.org/docs/challenge-types/#dns-01-challenge)

An exact TXT owner takes precedence over a CNAME inherited from a wildcard service record. The default TXT propagation timeout is 360 seconds. A deliberate CNAME at the challenge owner itself is a separate DNS-01 delegation. Legacy deployments that already use lego's explicit delegation mode can keep their existing override and authorize the destination zone. [Cloudflare wildcard DNS records](https://developers.cloudflare.com/dns/manage-dns-records/reference/wildcard-dns-records/) · [lego CNAME delegation](https://go-acme.github.io/lego/references/ref-options/#lego_disable_cname_support)

### Verify A, AAAA and CNAME issuance in staging

For an optional compatibility check, choose an active zone that your Cloudflare token can edit and replace `example.net` below. You can use dedicated DNS-only test records and one planned hostname.

| DNS record to verify | Type | Example value |
|---|---|---|
| `a-check.example.net` | A | `192.0.2.10` |
| `v6-check.example.net` | AAAA | `2001:db8::10` |
| `alias-check.example.net` | CNAME | `a-check.example.net` |
| `*.wild-check.example.net` | CNAME | `a-check.example.net` |

```bash
dig +short a-check.example.net A
dig +short v6-check.example.net AAAA
dig +short alias-check.example.net CNAME
dig +short probe.wild-check.example.net CNAME
```

Put this group in a separate file named `dns-smoke.yaml` so the test submits one certificate order.

```yaml
dns-smoke:
  - a-check.example.net
  - v6-check.example.net
  - alias-check.example.net
  - '*.wild-check.example.net'
  - planned-check.example.net
```

```bash
DOMAINS_FILE=./dns-smoke.yaml docker compose run --rm certgen --staging --only dns-smoke
```

Expect `5 Attempted`, `5 Issued` and `0 Failed` when the certificate is issued. Locate `certificates/a-check.example.net.crt` in the reported output path, replacing the container prefix `/ssl-certs` with `./ssl-certs` on the host. Use `openssl` on the host to compare the SANs.

```bash
openssl x509 -in /path/from/run/certificates/a-check.example.net.crt -noout -issuer -ext subjectAltName
```

### Command options

| Option | Description |
|---|---|
| `--list` | Validate the YAML and list the selected groups and domain counts |
| `--dry-run` | Print the selected domains, ACME endpoint and planned output paths |
| `--only FOLDER` | Select a group. Repeat to select multiple groups in YAML order. |
| `--staging` / `--production` | Override `PRODUCTION` for one invocation |
| `--config FILE` | Select a YAML file inside the container. The default mount target is `/app/domains.yaml`. |
| `--output DIRECTORY` | Select a directory inside the container. Mount it and grant write access to the configured UID/GID. The default is `/ssl-certs`. |

### Issuance report

Each row counts the DNS names of one selected certificate. The footer totals those counts, followed by counts of certificate requests. Terminal output uses ANSI colors and wraps long group names to the terminal width. The table uses ASCII borders and keeps emojis outside the grid.

| Column | Meaning |
|---|---|
| Attempted | Names passed to lego in the certificate request |
| Issued | Names covered by the successfully issued certificate |
| Failed | Names passed to lego in a request that failed |
| Skipped | Names in a group that could not reach lego, for example due to an unwritable output directory |

The request issues one certificate for all its names. A failed order counts all requested names as failed even when some individual DNS challenges passed. Failed or skipped groups produce a nonzero exit status after the report. Check the preceding lego or setup error for the cause.

### 🧪 Run the tests

From the repository root on Linux, use a C++17 compiler, libyaml development headers and standard command-line utilities. The ANSI tests use the util-linux `script` command to create a pseudo-terminal.

```sh
sh tests/run.sh
```

The 38 offline cases cover YAML and CLI validation, credential handling, the DNS-01 arguments for A, AAAA, CNAME, wildcards and planned names, concurrent output paths, mixed certificate results, ANSI alignment and release archive checksums. They use local fixtures for lego and curl. Use the staging example above to verify actual issuance against a Cloudflare zone you control.

---

## 🖥️ Terminal examples

Two certificate groups with ordinary names, subdomains and nested wildcards.

```yaml
example-com:
  - example.com
  - www.example.com
  - api.example.com
  - '*.apps.example.com'

example-org:
  - example.org
  - docs.example.org
  - api.staging.example.org
  - '*.staging.example.org'
```

The terminal below shows a simulated staging run using the bundled example. The first order succeeds and the second hits a TXT propagation timeout. The ANSI table reports eight names attempted, four issued and four failed.

![Actual ANSI terminal capture of a simulated certificate run for example.com and example.org](assets/certgen-terminal-example.png)

[Example YAML](examples/terminal.domains.yaml)

---

## :key: Cloudflare API token

Create a token at [dash.cloudflare.com/profile/api-tokens](https://dash.cloudflare.com/profile/api-tokens) with the following permissions:

| Resource | Permission |
|---|---|
| Zone - DNS | Edit |
| Zone - Zone | Read |

Scope the token to the zones for the groups you plan to issue.

---

## 🔐 Runtime permissions

The entrypoint starts as root to prepare the standard output directory, then the YAML reader and lego run as the configured UID/GID. Set them to an identity that can read the mounted YAML and write the certificate output.

| Capability | Why it is needed |
|---|---|
| `CHOWN` | Set ownership of the standard output directory |
| `SETUID` | Switch to the configured UID |
| `SETGID` | Switch to the configured GID |

Compose uses these three capabilities with `no-new-privileges:true`. Plain Docker uses the default capabilities. With `--env-file`, the Docker daemon exposes the environment to users who can inspect the running container. Compose passes the token through `/run/secrets/cloudflare_api_token`. Keep `.env`, `certgen.env` and `ssl-certs/` local.

---

## 📁 Project files

| File | Purpose |
|---|---|
| `build/Dockerfile` | Alpine downloader, C++ builder and minimal runtime target |
| `build/download_lego.sh` | Platform-specific lego download and checksum |
| `build/entrypoint.sh` | CLI, output directory and privilege drop |
| `build/src/` | C++ YAML validator, runtime helpers and ANSI report |
| `build/run_lego.sh` | One lego request per selected certificate |
| `build/validate_env.sh` | Configuration validation |
| `compose.yml` | One-shot Compose service with a Docker secret |
| `example.env` | Example environment settings |
| `template.domains.yaml` | Sample certificate groups |
| `tests/run.sh` | Offline regression tests |
| `examples/terminal.domains.yaml` | Names used in the terminal demonstration |
---

## :information_source: Notes

- **Staging by default.** `PRODUCTION=false` uses Let's Encrypt staging. Test there first. Browsers do not trust certificates from staging.
- **One certificate per group.** Each invocation processes the groups in YAML order. If a group fails, the run continues and reports a nonzero exit status after the summary. Retry individual groups with `--only`.
- **Fresh output on every issuance.** Rerunning creates a new timestamp directory per group.
- **Rate limits.** Production limits apply to both registered domains and identical sets of DNS names. See the [Let's Encrypt rate limits](https://letsencrypt.org/docs/rate-limits/).

---

## 🔐 Alpine image findings and lego version

Docker Scout reported the seven findings below for the previous Alpine 3.24.4 image. The scores and target revisions describe that scan. The report covers upstream user-space libraries packaged by Alpine. Version 4.0.0 assembles a fresh filesystem from the Alpine 3.24 repositories. Scan the rebuilt image digest to see its findings.

| CVE | Package seen in the scan | Docker Scout score | Fix revision shown by Scout |
|---|---|---|---|
| CVE-2026-4738 | `zlib 1.3.2-r1` | 9.4 (Critical) | `1.4-r0` |
| CVE-2026-107891 | `musl 1.2.6-r3` | 8.1 (High) | `1.2.6-r6` |
| CVE-2026-95692 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |
| CVE-2026-88823 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |
| CVE-2026-95691 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |
| CVE-2026-88822 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |
| CVE-2026-107496 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |

### CVE-2026-4738 · zlib

This issue involves out-of-bounds pointer arithmetic in the contributed `infback9` Deflate64 decoder. The report originally described the copy bundled with GDAL. The zlib maintainer notes that pointers return to an in-bounds position before use and that the arithmetic can trap on capability hardware such as CHERI. Exposure depends on whether a process executes the affected decoder. Alpine's 3.24 security tracker lists `1.4-r0` as the fixed package revision. [zlib release notes](https://zlib.net/) · [Alpine security tracker](https://security.alpinelinux.org/vuln/CVE-2026-4738)

### CVE-2026-107891 · musl libc

Integer overflows in `regcomp()` can cause undersized allocations and heap writes outside the allocated buffer when a process compiles an untrusted regular expression. The upstream advisory describes crashes and possible code execution under application-dependent conditions. Alpine's security tracker lists `1.2.6-r3` as fixed on **3.24 stable**, which matches the revision shown in the scan. The `1.2.6-r6` target applies to **edge**. [musl advisory](https://www.openwall.com/lists/musl/2026/10/21/1) · [Alpine security tracker](https://security.alpinelinux.org/vuln/CVE-2026-107891)

OpenSSL's 10 November 2026 advisory assigns Low severity to the five OpenSSL entries. The table's OpenSSL APK revision is the fix target reported by Docker Scout. [OpenSSL advisory](https://openssl-library.org/news/secadv/20261110.txt)

A future release will use a newer Alpine image and verify the applicable fixes against its digest. This release keeps the older lego `v4.34.0` for simplicity. The plan is to move to lego `5.X.Y` after testing its CLI and Cloudflare DNS-01 workflow on the supported architectures. [lego releases](https://github.com/go-acme/lego/releases)

---

## :page_facing_up: Licence

This project is a thin wrapper around [lego](https://github.com/go-acme/lego), which is licenced under the [MIT Licence](https://github.com/go-acme/lego/blob/master/LICENSE). The C++ validator uses [libyaml](build/licenses/libyaml.txt) under its MIT license.
