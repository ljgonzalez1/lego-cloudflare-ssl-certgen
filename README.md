# :closed_lock_with_key: lego-cloudflare-certgen

![Project banner](assets/readme-banner.png)

[![Docker Hub](https://img.shields.io/badge/Docker%20Hub-ljgonzalez%2Flego--cloudflare--certgen-2496ED?logo=docker&logoColor=white)](https://hub.docker.com/r/ljgonzalez/lego-cloudflare-certgen)
[![Docker pulls](https://img.shields.io/docker/pulls/ljgonzalez/lego-cloudflare-certgen?logo=docker&logoColor=white)](https://hub.docker.com/r/ljgonzalez/lego-cloudflare-certgen/tags)
[![Image](https://img.shields.io/badge/image-v4.0.1-blue)](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.0.1)
[![lego](https://img.shields.io/badge/lego-v4.34.0-informational)](https://github.com/go-acme/lego/releases/tag/v4.34.0)
[![Tests](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/actions/workflows/tests.yml/badge.svg)](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/actions/workflows/tests.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)

> One-shot Docker container that issues **Let's Encrypt** certificates with [lego](https://github.com/go-acme/lego) **v4.34.0** and the **Cloudflare DNS-01** challenge. No open ports and no web server: list your DNS names in a YAML file, run the container, collect the certificates. The container prints a summary and exits.

---

## :bookmark_tabs: Contents

- [Features](#sparkles-features)
- [Requirements](#clipboard-requirements)
- [Cloudflare API token](#key-cloudflare-api-token)
- [Quick start](#rocket-quick-start)
- [Output layout](#open_file_folder-output-layout)
- [Configuration](#gear-configuration)
- [Issuance report](#bar_chart-issuance-report)
- [Image tags and platforms](#label-image-tags-and-platforms)
- [Build the image yourself](#hammer_and_wrench-build-the-image-yourself)
- [Tests](#test_tube-tests)
- [Security and permissions](#shield-security-and-permissions)
- [Project files](#file_folder-project-files)
- [Notes](#information_source-notes)
- [License](#page_facing_up-license)

---

## :sparkles: Features

- **One certificate per group.** Each top-level key in `domains.yaml` is one certificate and one output folder. Its list holds every DNS name on that certificate.
- **Wildcards and any record type.** DNS-01 works the same for names with A, AAAA or CNAME records, for quoted wildcards such as `"*.example.com"`, and for names that have no record yet.
- **Secrets-safe Compose workflow.** Compose hands the Cloudflare token to the container as a [Docker secret](https://docs.docker.com/compose/use-secrets/) file. lego reads that file directly.
- **Staging first.** Runs use Let's Encrypt staging unless you pass `--production` or set `PRODUCTION=true`.
- **Clear results.** A final table lists attempted, issued, failed and skipped names for every group, with totals.
- **Small, non-root runtime.** A `scratch` image with a minimal Alpine 3.24 filesystem. The entrypoint drops to your UID/GID before reading the YAML or calling lego.
- **Four architectures.** `linux/amd64`, `linux/386`, `linux/arm/v7` and `linux/arm64`.

---

## :clipboard: Requirements

- Docker Engine. The Docker Compose plugin is optional but recommended.
- One or more zones managed by Cloudflare.
- A Cloudflare API token, created in the [next section](#key-cloudflare-api-token).
- A contact e-mail address for your Let's Encrypt account.

---

## :key: Cloudflare API token

Create a token at [dash.cloudflare.com/profile/api-tokens](https://dash.cloudflare.com/profile/api-tokens) with these permissions:

| Resource | Permission |
|---|---|
| Zone · DNS | Edit |
| Zone · Zone | Read |

Limit the token to the zones you plan to issue certificates for. lego uses it to find each zone, create a temporary `_acme-challenge` TXT record and delete it afterwards. [Cloudflare token guide](https://developers.cloudflare.com/fundamentals/api/get-started/create-token/)

---

## :rocket: Quick start

The image is published on Docker Hub as [`ljgonzalez/lego-cloudflare-certgen`](https://hub.docker.com/r/ljgonzalez/lego-cloudflare-certgen). You don't need to clone or build anything. Pin the image to **`v4.0.1`** so that every run uses the same image.

### :one: Prepare a working directory

Download the three sample files:

```bash
mkdir lego-certgen && cd lego-certgen
base=https://raw.githubusercontent.com/ljgonzalez1/lego-cloudflare-ssl-certgen/master
curl -fsSLO "$base/compose.yml"
curl -fsSL -o domains.yaml "$base/template.domains.yaml"
(umask 077; curl -fsSL -o .env "$base/example.env")
mkdir -p ssl-certs
```

Or clone the repository and copy the templates:

```bash
git clone https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen.git
cd lego-cloudflare-ssl-certgen
cp template.domains.yaml domains.yaml
test -e .env || (umask 077; cp example.env .env)
mkdir -p ssl-certs
```

### :two: Fill in `.env`

Open `.env` and set at least these values. Read the [Let's Encrypt Terms of Service](https://letsencrypt.org/repository/) before you set `ACCEPT_LEGO_TOS=true`.

```dotenv
EMAIL=you@example.com
CLOUDFLARE_API_TOKEN=your-cloudflare-token
ACCEPT_LEGO_TOS=true
PRODUCTION=false
TZ=Etc/UTC
UID=1000
GID=1000
```

Set `UID` and `GID` to the host user that should own the certificates (`id -u` and `id -g`). Then make the file private:

```bash
chmod 600 .env
```

All settings are listed in [Environment variables](#environment-variables).

### :three: List your names in `domains.yaml`

Replace the sample names with your own. Each top-level key becomes one certificate:

```yaml
certificateA:
  - example.com
  - www.example.com
  - "*.example.com"

certificateB:
  - example.net
  - "*.example.net"
```

See [`domains.yaml` format](#domainsyaml-format) for the rules.

### :four: Run the container

Use **either** Docker Compose **or** `docker run`. Both use the `v4.0.1` image from Docker Hub.

#### :whale: Docker Compose (recommended)

The `compose.yml` you downloaded looks like this:

```yaml
name: lego-cloudflare-certgen

services:
  certgen:
    image: "${CERTGEN_IMAGE:-ljgonzalez/lego-cloudflare-certgen:v4.0.1}"
    env_file:
      - "${CERTGEN_ENV_FILE:-./.env}"
    # The token is passed as a secret file instead of an environment variable.
    environment:
      CLOUDFLARE_API_TOKEN: ""
    volumes:
      - type: bind
        source: "${DOMAINS_FILE:-./domains.yaml}"
        target: /app/domains.yaml
        read_only: true
        bind:
          create_host_path: false
      - ./ssl-certs:/ssl-certs
    secrets:
      - cloudflare_api_token
    restart: "no"
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - SETUID
      - SETGID

secrets:
  cloudflare_api_token:
    environment: CLOUDFLARE_API_TOKEN
```

Preview the configuration, then issue the certificates:

```bash
# Check the YAML and list the groups
docker compose run --rm certgen --list

# Show the names, ACME server and output paths without contacting Let's Encrypt
docker compose run --rm certgen --dry-run

# Issue staging (untrusted) certificates
docker compose run --rm certgen

# Issue real, browser-trusted certificates
docker compose run --rm certgen --production
```

Compose reads `CLOUDFLARE_API_TOKEN` from `.env` and mounts it at `/run/secrets/cloudflare_api_token`. The other settings come from `.env` too. To point at different files, set `DOMAINS_FILE=./other.yaml` or `CERTGEN_ENV_FILE=./certgen.env` before the command.

#### :package: `docker run --rm`

```bash
# Issue staging certificates
docker run --rm -t --env-file .env \
  -v ./domains.yaml:/app/domains.yaml:ro \
  -v ./ssl-certs:/ssl-certs \
  ljgonzalez/lego-cloudflare-certgen:v4.0.1

# Issue production certificates for one group
docker run --rm -t --env-file .env \
  -v ./domains.yaml:/app/domains.yaml:ro \
  -v ./ssl-certs:/ssl-certs \
  ljgonzalez/lego-cloudflare-certgen:v4.0.1 --production --only certificateA
```

The [command options](#command-options) work after the image name, for example `--list` or `--dry-run`. `-t` enables the colored output.

- **Relative bind paths** (`./domains.yaml`) need Docker Engine 23 or later. On older versions, use `"$PWD/domains.yaml:/app/domains.yaml:ro"` and `"$PWD/ssl-certs:/ssl-certs"`.
- **Keep the token out of `docker inspect`.** With `--env-file`, the token is visible to anyone who can inspect the container. To avoid that, mount it as a file, the way Compose does:

  ```bash
  (umask 077; printf '%s\n' 'your-cloudflare-token' > cloudflare_api_token)

  docker run --rm -t --env-file .env -e CLOUDFLARE_API_TOKEN= \
    -v ./domains.yaml:/app/domains.yaml:ro \
    -v ./ssl-certs:/ssl-certs \
    -v ./cloudflare_api_token:/run/secrets/cloudflare_api_token:ro \
    ljgonzalez/lego-cloudflare-certgen:v4.0.1
  ```

  `-e CLOUDFLARE_API_TOKEN=` overrides any token left in `.env`, so the container reads the file. The configured `UID` must be able to read the file.

---

## :open_file_folder: Output layout

Each run creates a new timestamped folder per group under `./ssl-certs/<group>/`:

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

- The first name in a group sets the file names (`example.com.*` above).
- Folder names use `TZ`. Set `TZ=America/Santiago` to match the original script's names.
- The `expires_` part of the folder name is an estimate. To read the exact expiry date:

  ```bash
  openssl x509 -in example.com.crt -noout -enddate
  ```

---

## :gear: Configuration

### Environment variables

Settings are optional unless marked **required**.

| Variable | Default | Required | Description |
|---|---|---|---|
| `EMAIL` | - | :heavy_check_mark: | Contact e-mail for the Let's Encrypt account |
| `CLOUDFLARE_API_TOKEN` | - | :heavy_check_mark: | Cloudflare DNS API token. Compose turns it into a secret file. `docker run` also accepts the legacy `CLOUDFLARE_API_KEY` variable. |
| `ACCEPT_LEGO_TOS` | `false` | :heavy_check_mark: | Set `true` after reading the [Let's Encrypt Terms of Service](https://letsencrypt.org/repository/) |
| `PRODUCTION` | `false` | :x: | `true` issues browser-trusted certificates. `false` uses staging. |
| `TZ` | `Etc/UTC` | :x: | IANA timezone used in folder names and log timestamps |
| `PROPAGATION_SECONDS` | `360` | :x: | Maximum wait for TXT propagation per challenge, in seconds |
| `ORDER_TIMEOUT_SECONDS` | `auto` | :x: | Time limit for each certificate request. `auto` allows `PROPAGATION_SECONDS` per name plus 10 minutes, and at least 1 hour. `0` disables the limit. |
| `DNS_RESOLVERS` | `1.1.1.1:53,8.8.8.8:53,1.0.0.1:53` | :x: | Comma-separated `host:port` resolvers used to check TXT visibility |
| `UID` | `1000` | :x: | Non-zero UID that owns the certificates and ACME account data |
| `GID` | `1000` | :x: | Non-zero GID that owns the certificates and ACME account data |

These variables are read by Compose on the host, not by the container:

| Variable | Default | Description |
|---|---|---|
| `DOMAINS_FILE` | `./domains.yaml` | Host YAML file mounted at `/app/domains.yaml` |
| `CERTGEN_IMAGE` | `ljgonzalez/lego-cloudflare-certgen:v4.0.1` | Image used by the Compose service |
| `CERTGEN_ENV_FILE` | `./.env` | Settings file passed to the container |

### Command options

Add these after `docker compose run --rm certgen` or after the image name in `docker run`.

| Option | Description |
|---|---|
| `--list` | Validate the YAML and list the selected groups with their name counts |
| `--dry-run` | Print the selected names, ACME server and planned output paths. Nothing is requested. |
| `--only FOLDER` | Process only this group. Repeat it to select several groups, in YAML order. |
| `--staging` / `--production` | Override `PRODUCTION` for this run |
| `--config FILE` | Read another YAML file inside the container. Default: `/app/domains.yaml`. |
| `--output DIRECTORY` | Write to another directory inside the container. It must be mounted and writable by `UID`/`GID`. Default: `/ssl-certs`. |
| `-h`, `--help` | Show the usage text |

### `domains.yaml` format

Copy [`template.domains.yaml`](template.domains.yaml) for a larger example with eight groups.

```yaml
certificateA:
  - example.com
  - www.example.com
  - "*.example.com"

certificateB:
  - example.net
  - "*.example.net"
```

- **Quote wildcards** (`"*.example.com"`) so YAML reads them as strings.
- **Group names** become folder names. Use letters, numbers, hyphens or underscores, starting with a letter or number.
- **Up to 100 distinct names** per certificate. The same name can appear in several groups.
- **The first name** in a group sets lego's certificate file name.
- A wildcard covers exactly one extra label. `*.example.com` covers `www.example.com` but not `example.com` or `a.b.example.com`.

### Hostnames and DNS-01

List every name you want on the certificate, including quoted wildcards. Every name takes the same DNS-01 path, whether it has an A, AAAA or CNAME record or no record yet. lego writes a temporary `_acme-challenge` TXT record through the Cloudflare API, Let's Encrypt checks it, and lego removes it. [Let's Encrypt DNS-01](https://letsencrypt.org/docs/challenge-types/#dns-01-challenge)

An exact TXT owner takes precedence over a CNAME inherited from a wildcard service record. A deliberate CNAME at the challenge owner itself is a separate DNS-01 delegation. Older deployments that already use lego's explicit delegation mode can keep their `LEGO_DISABLE_CNAME_SUPPORT=false` override and authorize the destination zone. [Cloudflare wildcard records](https://developers.cloudflare.com/dns/manage-dns-records/reference/wildcard-dns-records/) · [lego CNAME delegation](https://go-acme.github.io/lego/references/ref-options/#lego_disable_cname_support)

---

## :bar_chart: Issuance report

When the run ends, a table shows the DNS names of each selected certificate, followed by totals.

| Column | Meaning |
|---|---|
| Attempted | Names passed to lego in the certificate request |
| Issued | Names covered by the certificate that was issued |
| Failed | Names in a request that failed |
| Skipped | Names in a group that never reached lego, for example because the output directory was not writable |

Each request issues one certificate for all its names. A failed order counts every name as failed, even if some DNS challenges passed. If any group fails or is skipped, the run still finishes the other groups, then exits with a nonzero status. The lego or setup error above the table gives the cause.

In a terminal the table is colored and adapts to the terminal width. It uses ASCII borders and keeps emojis outside the grid.

### :desktop_computer: Terminal example

This simulated staging run uses [`examples/terminal.domains.yaml`](examples/terminal.domains.yaml):

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

The first order succeeds. The second hits a TXT propagation timeout. The report shows eight names attempted, four issued and four failed.

![ANSI terminal capture of a simulated certificate run for example.com and example.org](assets/certgen-terminal-example.png)

---

## :label: Image tags and platforms

| Tag | Use it when |
|---|---|
| `v4.0.1` | You want a fixed image. **Recommended.** |
| `v4.0`, `v4` | You want future patch or minor releases of version 4 |
| `latest` | You always want the newest release |
| `alpine3.24.4-lego4.34.0-v4.0.1` | You want the base and lego versions in the tag name |

Every `v4.0.1` tag points to the same multi-architecture image for `linux/amd64`, `linux/386`, `linux/arm/v7` and `linux/arm64`. Docker pulls the right variant automatically. Older releases (v1 to v3) are still available. See [all tags on Docker Hub](https://hub.docker.com/r/ljgonzalez/lego-cloudflare-certgen/tags).

---

## :hammer_and_wrench: Build the image yourself

You only need this to change the code. A normal `docker build` produces an image for your own machine's architecture.

```bash
git clone https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen.git
cd lego-cloudflare-ssl-certgen

docker build -t lego-cloudflare-certgen:local ./build
```

Check the result:

```bash
docker run --rm --entrypoint lego lego-cloudflare-certgen:local --version
docker run --rm -v ./template.domains.yaml:/app/domains.yaml:ro lego-cloudflare-certgen:local --list
```

Use your build with Compose or `docker run`:

```bash
CERTGEN_IMAGE=lego-cloudflare-certgen:local docker compose run --rm certgen --dry-run
```

The build needs BuildKit, the default builder since Docker Engine 23. It also needs network access to Docker Hub, GitHub Releases and the Alpine package mirrors.

### :feather: What the build does

1. **Downloader stage.** Downloads the official lego `v4.34.0` archive for the target CPU and verifies its SHA-256 checksum.
2. **Builder stage.** Compiles `certgen-core` (C++17, statically linked with libyaml) and builds a minimal Alpine 3.24.4 filesystem with musl, the CA bundle, timezone data, `su-exec` and Dash as `/bin/sh`.
3. **Final stage.** Starts from `scratch` and copies in only that filesystem, the scripts, `certgen-core` and lego. BusyBox, Bash, Python, the compiler and curl stay out of the image.
4. **Self-check.** Runs lego as the non-root user, checks its version and architecture, then runs `--list`, `--dry-run` and the report against a sample file. No certificate is requested.

Show the image size in MiB:

```sh
docker image inspect --format '{{.Size}}' lego-cloudflare-certgen:local |
  awk '{printf "%.1f MiB\n", $1 / 1048576}'
```

<details>
<summary><b>:gear: Advanced: multi-architecture builds with Buildx</b></summary>

<br>

Only release maintainers need this, or anyone who wants images for other CPUs. It needs Docker Buildx and, for non-native targets, QEMU emulators registered on the host.

| Target | Buildx platform | Official lego archive |
|---|---|---|
| AMD64 / x86-64 | `linux/amd64` | `lego_v4.34.0_linux_amd64.tar.gz` |
| i386 / 32-bit x86 | `linux/386` | `lego_v4.34.0_linux_386.tar.gz` |
| ARMv7 / Debian armhf | `linux/arm/v7` | `lego_v4.34.0_linux_armv7.tar.gz` |
| ARMv8 / AArch64 | `linux/arm64` | `lego_v4.34.0_linux_arm64.tar.gz` |

The ARMv7 image uses the Alpine `armv7` userspace. Debian calls that architecture `armhf`. [Alpine image platforms](https://hub.docker.com/_/alpine) · [lego v4.34.0](https://github.com/go-acme/lego/releases/tag/v4.34.0)

**Create a builder** (once):

```bash
docker buildx create --name lego-multiarch --driver docker-container --use
docker buildx inspect lego-multiarch --bootstrap
```

Check that the `Platforms` line lists all four targets. On Linux, register any missing emulators and inspect again. [Docker multi-platform builds](https://docs.docker.com/build/building/multi-platform/)

```bash
docker run --privileged --rm tonistiigi/binfmt --install arm64,arm,386
docker buildx inspect lego-multiarch --bootstrap
```

**Build one platform and load it** into the local Engine for testing:

```bash
docker buildx build --builder lego-multiarch --platform linux/arm64 \
  --progress=plain --load -t lego-cloudflare-certgen:local-arm64 ./build
```

**Build every platform with its own local tag:**

```sh
for arch in amd64 386 arm/v7 arm64; do
  suffix=$(printf '%s' "$arch" | tr / -)
  docker buildx build --builder lego-multiarch --platform "linux/$arch" \
    --progress=plain --load -t "lego-cloudflare-certgen:local-$suffix" ./build || break
done
```

**Push one multi-architecture image** to a registry you control:

```bash
docker buildx build --builder lego-multiarch \
  --platform linux/amd64,linux/386,linux/arm/v7,linux/arm64 \
  -t <registry>/<user>/lego-cloudflare-certgen:<tag> --push ./build
```

</details>

---

## :test_tube: Tests

`tests/run.sh` holds 41 offline tests. They cover YAML and CLI validation, credential handling, the DNS-01 arguments for A, AAAA, CNAME, wildcard and planned names, rate limits and time limits, concurrent output paths, mixed certificate results, ANSI alignment and the release checksums. Local stand-ins replace lego and curl, so nothing touches the network. GitHub Actions runs them on every push and pull request.

On Debian or Ubuntu:

```sh
sudo apt-get install -y g++ libyaml-dev
sh tests/run.sh
```

Or inside a throwaway Alpine container, without installing anything on the host:

```sh
docker run --rm -v "$PWD:/src:ro" -w /src alpine:3.24.4 \
  sh -c 'apk add --no-cache build-base yaml-dev util-linux-misc >/dev/null && sh tests/run.sh'
```

The last line should read `41 passed, 0 failed, 0 skipped`. Three terminal tests need the util-linux `script` command. Without it they are skipped.

The scripts in `build/` must keep their executable bit in Git, because `entrypoint.sh` runs `run_lego.sh` directly. If the tests stop with `Bail out! … is not executable`, restore the bit and commit:

```sh
git update-index --chmod=+x build/*.sh tests/run.sh
```

### :white_check_mark: End-to-end check in staging

Use this optional check to confirm real issuance for A, AAAA, CNAME, wildcard and planned names. Choose an active zone your token can edit and replace `example.net`. Create these DNS-only test records:

| DNS record | Type | Example value |
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

Save this group as `dns-smoke.yaml`, so the check submits a single order:

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

Expect `5 Attempted`, `5 Issued` and `0 Failed`. In the reported output path, replace the container prefix `/ssl-certs` with `./ssl-certs` and check the SANs:

```bash
openssl x509 -in ./ssl-certs/dns-smoke/<timestamp>/<request>/certificates/a-check.example.net.crt \
  -noout -issuer -ext subjectAltName
```

---

## :shield: Security and permissions

### Runtime permissions

The entrypoint starts as root only to prepare `/ssl-certs`. It then switches to the configured `UID`/`GID`, and the YAML reader and lego run as that user. That identity must be able to read the mounted YAML and write the output.

| Capability | Why it is needed |
|---|---|
| `CHOWN` | Set ownership of the standard output directory |
| `SETUID` | Switch to the configured UID |
| `SETGID` | Switch to the configured GID |

Compose keeps only these three capabilities and adds `no-new-privileges:true`. Plain `docker run` uses Docker's [default capabilities](https://docs.docker.com/engine/containers/run/#runtime-privilege-and-linux-capabilities), which include them. To run `docker run` with the same restrictions as Compose, add:

```bash
--cap-drop ALL --cap-add CHOWN --cap-add SETUID --cap-add SETGID \
--security-opt no-new-privileges:true
```

### Secrets

- Compose passes the token as `/run/secrets/cloudflare_api_token`. It never appears in the container's environment.
- `docker run --env-file` exposes the token to anyone who can run `docker inspect`. Use the [secret-file variant](#package-docker-run---rm) to avoid that.
- Keep `.env`, `certgen.env`, `cloudflare_api_token` and `ssl-certs/` out of version control. The provided `.gitignore` already excludes `.env`, `certgen.env`, `ssl-certs` and `/domains.yaml`.

### Alpine image findings

Docker Scout reported the seven findings below for the previous Alpine 3.24.4 image. The scores and fix revisions come from that scan, which covers user-space libraries packaged by Alpine. Version 4.0.1 assembles a fresh filesystem from the Alpine 3.24 repositories. Scan the rebuilt image digest to see its current findings.

| CVE | Package seen in the scan | Docker Scout score | Fix revision shown by Scout |
|---|---|---|---|
| CVE-2026-4738 | `zlib 1.3.2-r1` | 9.4 (Critical) | `1.4-r0` |
| CVE-2026-107891 | `musl 1.2.6-r3` | 8.1 (High) | `1.2.6-r6` |
| CVE-2026-95692 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |
| CVE-2026-88823 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |
| CVE-2026-95691 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |
| CVE-2026-88822 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |
| CVE-2026-107496 | `openssl 3.5.9-r0` | N/A | `3.5.9-r1` |

<details>
<summary><b>Details for each finding</b></summary>

<br>

**CVE-2026-4738 · zlib.** Out-of-bounds pointer arithmetic in the contributed `infback9` Deflate64 decoder. The report originally described the copy bundled with GDAL. The zlib maintainer notes that pointers return to an in-bounds position before use, and that the arithmetic can trap on capability hardware such as CHERI. Exposure depends on whether a process runs the affected decoder. Alpine's 3.24 security tracker lists `1.4-r0` as fixed. [zlib release notes](https://zlib.net/) · [Alpine security tracker](https://security.alpinelinux.org/vuln/CVE-2026-4738)

**CVE-2026-107891 · musl libc.** Integer overflows in `regcomp()` can cause undersized allocations and heap writes out of bounds when a process compiles an untrusted regular expression. The upstream advisory describes crashes and possible code execution under conditions that depend on the application. Alpine's tracker lists `1.2.6-r3` as fixed on **3.24 stable**, which matches the scanned revision. The `1.2.6-r6` target applies to **edge**. [musl advisory](https://www.openwall.com/lists/musl/2026/10/21/1) · [Alpine security tracker](https://security.alpinelinux.org/vuln/CVE-2026-107891)

**OpenSSL.** The 10 November 2026 advisory rates the five OpenSSL entries as Low severity. The fix revision in the table is the one Docker Scout reported. [OpenSSL advisory](https://openssl-library.org/news/secadv/20261110.txt)

</details>

A future release will use a newer Alpine image and check the fixes against its digest. This release stays on lego `v4.34.0` for simplicity. The plan is to move to lego `5.X.Y` after its CLI and the Cloudflare DNS-01 workflow are tested on all supported architectures. [lego releases](https://github.com/go-acme/lego/releases)

---

## :file_folder: Project files

| File | Purpose |
|---|---|
| `compose.yml` | One-shot Compose service with a Docker secret |
| `example.env` | Sample environment settings |
| `template.domains.yaml` | Sample certificate groups |
| `examples/terminal.domains.yaml` | Names used in the terminal example |
| `build/Dockerfile` | Downloader, C++ builder and minimal runtime stages |
| `build/download_lego.sh` | Downloads lego for the target platform and verifies its checksum |
| `build/entrypoint.sh` | CLI parsing, output directory and privilege drop |
| `build/validate_env.sh` | Logging and configuration validation |
| `build/run_lego.sh` | One lego request per selected certificate |
| `build/src/` | C++ YAML validator, runtime helpers, lego watchdog and ANSI report |
| `tests/run.sh` | Offline regression tests |
| `.github/workflows/tests.yml` | Runs the tests on GitHub Actions |

---

## :information_source: Notes

- **Staging by default.** Without `--production` or `PRODUCTION=true`, certificates come from Let's Encrypt staging, which browsers do not trust. Test there first.
- **One certificate per group.** Groups run in YAML order. If one fails, the rest still run and the exit status is nonzero. Retry a single group with `--only`.
- **Fresh output every time.** Each run creates a new timestamped folder per group. Nothing is overwritten.
- **Renewal.** The container issues new certificates and exits. Schedule it, for example with cron or a systemd timer, to renew before expiry.
- **Rate limits.** Production limits apply per registered domain and per identical set of names. Every run requests new certificates, so repeated production runs for the same group use up the limit of 5 per week quickly. Test with staging first. See [Let's Encrypt rate limits](https://letsencrypt.org/docs/rate-limits/).
- **No waiting on rate limits.** When Let's Encrypt answers with a rate limit, lego would sleep until the `Retry-After` time, often many hours. The container stops that request at once, marks the group as failed and continues with the next one. The lego output above the summary shows when to retry.
- **Time limit per certificate.** If a request takes longer than `ORDER_TIMEOUT_SECONDS`, lego is stopped and the group is marked as failed. A request stopped while waiting for propagation can leave `_acme-challenge` TXT records in Cloudflare. Delete them by hand.

---

## :page_facing_up: License

This project is released under the [MIT License](LICENSE). It is a thin wrapper around [lego](https://github.com/go-acme/lego), which is also [MIT licensed](https://github.com/go-acme/lego/blob/master/LICENSE). The C++ validator uses [libyaml](build/licenses/libyaml.txt) under its MIT license.
