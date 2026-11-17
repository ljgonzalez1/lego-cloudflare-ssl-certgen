# :closed_lock_with_key: lego-cloudflare-certgen

Issue Let's Encrypt certificates in Docker with [lego](https://github.com/go-acme/lego) **v4.34.0** and Cloudflare DNS-01. Define one certificate per group in `domains.yaml` and get a per-group report at the end of each run.

![Project banner](assets/readme-banner.png)

## :whale: Requirements

- Docker Engine. Install the Docker Compose plugin if you use Compose.
- A Cloudflare API token with **Zone → DNS → Edit** and **Zone → Zone → Read** for the relevant zones. [Create an API token](https://dash.cloudflare.com/profile/api-tokens)

## 🚀 Usage

From the project directory, copy `template.env` to `.env`. Set `EMAIL`, `CLOUDFLARE_API_TOKEN` and `ACCEPT_LEGO_TOS=true` after reviewing the [Let's Encrypt Terms of Service](https://letsencrypt.org/repository/).

```bash
test -e .env || (umask 077; cp template.env .env)
chmod 600 .env
mkdir -p ssl-certs
```

Create `domains.yaml` from [`template.domains.yaml`](template.domains.yaml) or use your existing file. Each key names a certificate group and its output directory. Quote wildcard names.

```yaml
web:
  - example.com
  - www.example.com
  - '*.apps.example.com'

api:
  - api.example.org
  - '*.staging.example.org'
```

### Docker run

```bash
docker run --rm -t --env-file .env \
  -v "$PWD/domains.yaml:/app/domains.yaml:ro" \
  -v "$PWD/ssl-certs:/ssl-certs" \
  ljgonzalez/lego-cloudflare-certgen:3.4.2
```

The default endpoint is Let's Encrypt staging. Append `--production` after the image name to request production certificates. The `-t` option enables ANSI colors.

### Docker Compose

Run from the directory containing `compose.yml`. Compose passes the Cloudflare token as a secret file.

```bash
docker compose run --rm certgen
```

```bash
docker compose run --rm certgen --production
```

For an existing setup that keeps separate container settings in `certgen.env`, keep the token in `.env` and select the settings file:

```bash
CERTGEN_ENV_FILE=./certgen.env docker compose run --rm certgen
```

### Preview your configuration

```bash
docker compose run --rm certgen --list
docker compose run --rm certgen --dry-run --only web
```

Certificates and ACME account data are written under `ssl-certs/<group>/`. The first DNS name in a group determines the certificate filename. Keep `.env`, `certgen.env` and the certificate output private.

## 🛠️ Build

The runtime uses **Alpine 3.24.4**. The build selects and verifies the official lego archive for the target architecture. The final image contains the lego executable and the packages needed at runtime.

| Target | Buildx platform |
|---|---|
| AMD64 / x86-64 | `linux/amd64` |
| i386 / 32-bit x86 | `linux/386` |
| ARMv7 / 32-bit ARM | `linux/arm/v7` |
| ARMv8 / AArch64 | `linux/arm64` |

### Build locally

Run from the project directory. Substitute a platform from the table as needed. Non-native targets require a builder with the appropriate emulation or a native build node. [Docker multi-platform builds](https://docs.docker.com/build/building/multi-platform/)

```bash
docker buildx build --platform linux/amd64 --load \
  --progress=plain \
  -t ljgonzalez/lego-cloudflare-certgen:3.4.2 ./build
```

```bash
docker run --rm --entrypoint lego \
  ljgonzalez/lego-cloudflare-certgen:3.4.2 --version

docker run --rm \
  -v "$PWD/template.domains.yaml:/app/domains.yaml:ro" \
  ljgonzalez/lego-cloudflare-certgen:3.4.2 --list
```

Inspect the size of the local image in MiB:

```bash
docker image inspect --format '{{.Size}}' ljgonzalez/lego-cloudflare-certgen:3.4.2 |
  awk '{printf "%.1f MiB\n", $1 / 1048576}'
```

## :gear: Configuration

| Variable | Default | Purpose |
|---|---|---|
| `EMAIL` | — | Required Let's Encrypt account email |
| `CLOUDFLARE_API_TOKEN` | — | Required Cloudflare credentials for DNS-01 |
| `ACCEPT_LEGO_TOS` | `false` | Set to `true` after reviewing the terms |
| `PRODUCTION` | `false` | Set to `true` to request production certificates |
| `TZ` | `Etc/UTC` | Timezone for output directory timestamps |
| `UID` / `GID` | `1000` / `1000` | Runtime identity and ownership of generated files |
| `PROPAGATION_SECONDS` | `360` | Maximum wait for TXT propagation per challenge |
| `DNS_RESOLVERS` | `1.1.1.1:53,8.8.8.8:53,1.0.0.1:53` | Comma-separated resolvers |
| `DOMAINS_FILE` | `./domains.yaml` | Host YAML path for Compose |
| `CERTGEN_IMAGE` | `ljgonzalez/lego-cloudflare-certgen:3.4.2` | Image used by Compose |
| `CERTGEN_ENV_FILE` | `./.env` | Settings file used by Compose |

### DNS-01 and wildcard names

List the names you want on each certificate. The same request path works for A, AAAA and CNAME service records and for planned hostnames awaiting a service record. lego uses the Cloudflare token to create and clean up temporary `_acme-challenge` TXT records. An exact TXT owner takes precedence over an inherited wildcard CNAME. Existing explicit delegations at `_acme-challenge` can use lego's CNAME option with a token authorized for the destination zone. [Let's Encrypt DNS-01](https://letsencrypt.org/docs/challenge-types/#dns-01-challenge) · [lego CNAME option](https://go-acme.github.io/lego/references/ref-options/#lego_disable_cname_support)

Use up to 100 distinct names per group. Group names can contain letters, digits, hyphens and underscores and must start with a letter or digit.

### Command options

| Option | Purpose |
|---|---|
| `--list` | Validate YAML and list certificate groups |
| `--dry-run` | Preview domains, the ACME endpoint and output paths |
| `--only GROUP` | Select a group. Repeat to select several groups. |
| `--staging` / `--production` | Choose the ACME endpoint for this run |
| `--config FILE` | Set the YAML path inside the container |
| `--output DIRECTORY` | Set the output path inside the container |

## 🧪 Checks

Run the 34-test offline suite with Bash, Python 3 and PyYAML. It covers YAML and CLI validation, Cloudflare token handling, output collisions, ANSI alignment and architecture-specific archive checksums.

```bash
python3 -B -m unittest discover -s tests -v
```

For an issuance check, choose an active Cloudflare zone. Put names for an A record, an AAAA record, a CNAME, a wildcard CNAME and a planned hostname in one group called `dns-smoke` in `dns-smoke.yaml`. Run:

```bash
DOMAINS_FILE=./dns-smoke.yaml docker compose run --rm certgen --staging --only dns-smoke
```

A successful order reports **5 Attempted**, **5 Issued** and **0 Failed**. Use `openssl x509 -in /path/to/certificate.crt -noout -ext subjectAltName` to inspect the SANs.

## 📋 Issuance report

The ANSI table counts attempted, issued, failed and skipped DNS names per certificate group. Each certificate covers the full group. A failed order counts every attempted name in that group as failed, and the command exits with a nonzero status after printing the summary.

### 🖥️ Terminal example

The simulated staging run below uses the [sample YAML for `example.com` and `example.org`](examples/terminal.domains.yaml), with ordinary names, subdomains and nested wildcards. It shows eight names attempted, four issued and four failed after a simulated TXT propagation timeout.

![ANSI terminal report for a simulated certificate run](assets/certgen-terminal-example.png)

## 🔐 Alpine image findings and lego version

Docker Scout reported the following entries in the base layer of the scanned Alpine 3.24.4 image. The entries concern user-space libraries packaged by Alpine. The scores and fix revisions below are those shown in the scan.

| CVE | Package in scan | Scout score | Fix revision shown by Scout |
|---|---|---|---|
| CVE-2026-4738 | `zlib 1.3.2-r1` | 9.4 (Critical) | `1.4-r0` |
| CVE-2026-107891 | `musl 1.2.6-r3` | 8.1 (High) | `1.2.6-r6` |
| CVE-2026-95692 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |
| CVE-2026-88823 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |
| CVE-2026-95691 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |
| CVE-2026-88822 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |
| CVE-2026-107496 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |

### CVE-2026-4738 · zlib

The issue involves out-of-bounds pointer arithmetic in the contributed `infback9` Deflate64 decoder. The report originally described the copy bundled with GDAL. The zlib maintainer notes that the affected pointers return to a valid range before use and that the arithmetic can trap on capability hardware such as CHERI. Exposure depends on whether a process executes the affected decoder. Alpine 3.24 lists `1.4-r0` as the fixed revision. [zlib release notes](https://zlib.net/) · [Alpine security tracker](https://security.alpinelinux.org/vuln/CVE-2026-4738)

### CVE-2026-107891 · musl libc

Integer overflows in `regcomp()` can lead to undersized allocations and out-of-bounds heap writes when a program compiles an untrusted regular expression. The upstream advisory describes crashes and possible code execution under application-dependent conditions. **Alpine 3.24 stable lists `1.2.6-r3` as fixed**, which matches the revision in the scan. The `1.2.6-r6` revision corresponds to **edge**. [musl advisory](https://www.openwall.com/lists/musl/2026/10/21/1) · [Alpine security tracker](https://security.alpinelinux.org/vuln/CVE-2026-107891)

OpenSSL's 10 November 2026 advisory assigns Low severity to the five OpenSSL entries. The APK revision in the table is the fix target shown by Docker Scout. [OpenSSL advisory](https://openssl-library.org/news/secadv/20261110.txt)

A future release will rebuild on a newer Alpine image with the applicable fixes and scan the resulting image digest. This release pins the older lego `v4.34.0` for simplicity. The project plans to migrate to lego `5.Y.X` after testing the CLI and Cloudflare DNS-01 on the supported architectures. [lego releases](https://github.com/go-acme/lego/releases)

## :page_facing_up: License

[MIT](LICENSE) · [lego license](https://github.com/go-acme/lego/blob/master/LICENSE)
