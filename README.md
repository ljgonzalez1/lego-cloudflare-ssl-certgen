# :closed_lock_with_key: lego-cloudflare-certgen

> One-shot Docker container for automated SSL/TLS certificate generation via **Let's Encrypt** using [lego](https://github.com/go-acme/lego) v4.34.0 and **Cloudflare DNS-01 challenge**. No HTTP ports or web server required.

---

![Project banner](assets/readme-banner.png)

---

## :sparkles: Why use this instead of running lego directly?

The container wraps lego with YAML configuration, Compose secrets and a certificate report.

1. **Keep your settings in `certgen.env` and your domains in YAML.** Each top-level key in `domains.yaml` represents one certificate and its output folder. Its list contains all the DNS names on that certificate.

2. **Use Docker Compose for a repeatable, secrets-safe workflow.** Docker Compose provides the Cloudflare API token as a [Docker secret](https://docs.docker.com/compose/use-secrets/). lego reads the secret file directly.

3. **See the outcome of each certificate request.** The final table lists attempted, issued, failed and skipped DNS names for every selected group, with totals for names and certificates.

---

## :whale: Requirements

- Docker Engine and the Docker Compose plugin if using Compose
- A Cloudflare [**DNS API token**](https://developers.cloudflare.com/fundamentals/api/get-started/create-token/) with `Zone > DNS > Edit` and `Zone > Zone > Read` for the relevant zones

---

## 🛠️ Build and publish

Version **3.2** runs on **Debian Bookworm Slim** with **lego v4.34.0**. Build from the directory that contains this README.

| Target | Buildx platform | Official lego archive |
|---|---|---|
| AMD64 / x86-64 | `linux/amd64` | `lego_v4.34.0_linux_amd64.tar.gz` |
| i386 / 32-bit x86 | `linux/386` | `lego_v4.34.0_linux_386.tar.gz` |
| ARMv7 / Debian armhf | `linux/arm/v7` | `lego_v4.34.0_linux_armv7.tar.gz` |
| ARMv8 / AArch64 | `linux/arm64` | `lego_v4.34.0_linux_arm64.tar.gz` |

The ARMv7 image uses the Debian `armhf` userspace. The download stage verifies the SHA-256 of the platform-specific archive, and the final stage checks the binary version and architecture, then runs `--list` and `--dry-run` on each target. [lego v4.34.0](https://github.com/go-acme/lego/releases/tag/v4.34.0)

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

### Build and try one platform locally

Use your target platform in place of `linux/amd64`. `--load` loads this single-platform image into the local Engine for testing.

```bash
docker buildx build --builder lego-multiarch --platform linux/amd64 \
  --progress=plain --load -t ljgonzalez/lego-cloudflare-certgen:3.2 ./build

docker run --rm --entrypoint lego ljgonzalez/lego-cloudflare-certgen:3.2 --version
docker run --rm -v "$PWD/template.domains.yaml:/app/domains.yaml:ro" \
  ljgonzalez/lego-cloudflare-certgen:3.2 --list
```


### Publish all four platforms under each tag

Create or open [ljgonzalez/lego-cloudflare-certgen on Docker Hub](https://hub.docker.com/r/ljgonzalez/lego-cloudflare-certgen). Use the `ljgonzalez` namespace and a Docker Hub access token with write permission when prompted by `docker login`. Select **Public** in Docker Hub if the images are intended for public pulls. [Docker Hub repository setup](https://docs.docker.com/docker-hub/repos/create/)

```bash
docker login --username ljgonzalez
docker buildx build --builder lego-multiarch \
  --platform linux/amd64,linux/386,linux/arm/v7,linux/arm64 \
  --progress=plain \
  -t ljgonzalez/lego-cloudflare-certgen:lego4.34.0-3.2 \
  -t ljgonzalez/lego-cloudflare-certgen:latest \
  -t ljgonzalez/lego-cloudflare-certgen:3.2 \
  --push ./build
```

One `--push` publishes the same multi-platform index under each tag. Publish 3.2 first and 3.3 second to place `latest` on 3.3. If re-publishing 3.2 later, omit its `latest` tag to keep it on 3.3.

```bash
for tag in lego4.34.0-3.2 latest 3.2; do
  docker buildx imagetools inspect "ljgonzalez/lego-cloudflare-certgen:$tag" || break
done
```

Each tag should list `linux/amd64`, `linux/386`, `linux/arm/v7` and `linux/arm64` (sometimes shown as `linux/arm64/v8`). Build attestations can appear as `unknown/unknown`. [Buildx image build options](https://docs.docker.com/reference/cli/docker/buildx/build/)


---

## 🚀 Quick start

Use the published 3.2 tag or load a locally built image under the same tag. Copy `template.env` to `certgen.env` and set `EMAIL` and `ACCEPT_LEGO_TOS=true`. Put your Cloudflare DNS API token in the project `.env` file as `CLOUDFLARE_API_TOKEN=your_token`.

```bash
test -e certgen.env || cp template.env certgen.env
mkdir -p ssl-certs
chmod 600 .env
docker compose run --rm certgen
```

Compose loads the token as a secret file and settings from `certgen.env`. Set `CERTGEN_IMAGE` to use another locally built tag. For plain Docker, pass both files in this order so `.env` supplies the token.

```bash
docker run --rm -t --env-file certgen.env --env-file .env \
  -v "$PWD/domains.yaml:/app/domains.yaml:ro" -v "$PWD/ssl-certs:/ssl-certs" \
  ljgonzalez/lego-cloudflare-certgen:3.2
```

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
| `LEGO_DISABLE_CNAME_SUPPORT` | `true` | :x: | `true` publishes TXT at the exact challenge name. Use `false` to follow explicit challenge CNAME delegations. |
| `ACCEPT_LEGO_TOS` | `false` | :heavy_check_mark: | Set `true` after reading the [Let's Encrypt Terms of Service](https://letsencrypt.org/repository/) |
| `UID` | `1000` | :x: | UID used to write certificates and ACME account data |
| `GID` | `1000` | :x: | GID used to write certificates and ACME account data |
| `DOMAINS_FILE` | `./domains.yaml` | :x: | Host YAML path for Compose |
| `CERTGEN_IMAGE` | `ljgonzalez/lego-cloudflare-certgen:3.2` | :x: | Image used by Compose |

### Certificate groups and wildcard domains

If you need a new `domains.yaml`, copy [`template.domains.yaml`](template.domains.yaml) and replace the sample DNS names. Keep the populated file local when uploading the project to GitHub.

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

### A, AAAA and CNAME records

A, AAAA and CNAME service records can coexist in the same certificate group, with ordinary names and quoted wildcards. DNS-01 validates each identifier through a temporary TXT record. For both `example.com` and `*.example.com`, the TXT owner is `_acme-challenge.example.com`. Add the names you want on the certificate to `domains.yaml` using the existing list format. [Let's Encrypt DNS-01 documentation](https://letsencrypt.org/docs/challenge-types/#dns-01-challenge)

By default, lego publishes TXT at the exact `_acme-challenge` name. The exact TXT takes precedence over a CNAME inherited from a wildcard DNS record. The 360-second propagation timeout allows caches to expire with Cloudflare's 300-second automatic TTL. Increase `PROPAGATION_SECONDS` if your records use a longer TTL. [Cloudflare wildcard DNS records](https://developers.cloudflare.com/dns/manage-dns-records/reference/wildcard-dns-records/) · [Cloudflare TTL](https://developers.cloudflare.com/dns/manage-dns-records/reference/ttl/)

If your DNS explicitly delegates `_acme-challenge` via a CNAME, set `LEGO_DISABLE_CNAME_SUPPORT=false` in `certgen.env` and grant the token access to the destination Cloudflare zone. This setting applies to all selected groups in that invocation. Select specific groups with `--only` when they use different challenge modes. [lego CNAME option](https://go-acme.github.io/lego/usage/cli/options/#lego_disable_cname_support)

### Verify A, AAAA and CNAME issuance in staging

Choose an active zone that your Cloudflare token can edit and replace `example.net` below. The names can be dedicated DNS-only test records.

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
```

```bash
DOMAINS_FILE=./dns-smoke.yaml docker compose run --rm certgen --staging --only dns-smoke
```

Expect `4 Attempted`, `4 Issued` and `0 Failed` when the certificate is issued. Locate `certificates/a-check.example.net.crt` in the reported output path, replacing the container prefix `/ssl-certs` with `./ssl-certs` on the host. Use `openssl` on the host to compare the SANs.

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

Run the local regression tests with `python3 -B -m unittest discover -s tests -v` from the repository root. They require Bash, Python 3 and PyYAML. The tests use local fixtures for lego and the release archives.

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
| `build/Dockerfile` | Multi-platform download and runtime stages |
| `build/download_lego.sh` | Platform-specific lego download and checksum |
| `build/entrypoint.sh` | CLI, output directory and privilege drop |
| `build/read_domains.py` | YAML validation |
| `build/run_lego.sh` | One lego request per selected certificate |
| `build/print_summary.py` | Terminal-aware ANSI table |
| `build/validate_env.sh` | Configuration validation |
| `compose.yml` | One-shot Compose service with a Docker secret |
| `template.env` and `example.env` | Local settings templates |
| `template.domains.yaml` | Sample certificate groups |
| `tests/` | Offline regression tests |
---

## :information_source: Notes

- **Staging by default.** `PRODUCTION=false` uses Let's Encrypt staging. Test there first. Browsers do not trust certificates from staging.
- **One certificate per group.** Each invocation processes the groups in YAML order. If a group fails, the run continues and reports a nonzero exit status after the summary. Retry individual groups with `--only`.
- **Fresh output on every issuance.** Rerunning creates a new timestamp directory per group.
- **Rate limits.** Production limits apply to both registered domains and identical sets of DNS names. See the [Let's Encrypt rate limits](https://letsencrypt.org/docs/rate-limits/).

---

## :page_facing_up: Licence

This project is a thin wrapper around [lego](https://github.com/go-acme/lego), which is licenced under the [MIT Licence](https://github.com/go-acme/lego/blob/master/LICENSE). Refer to the lego repository for its terms.
