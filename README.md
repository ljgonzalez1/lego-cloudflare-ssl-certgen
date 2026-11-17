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
  ljgonzalez/lego-cloudflare-certgen:3.3
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
  -t ljgonzalez/lego-cloudflare-certgen:3.3 ./build
```

```bash
docker run --rm --entrypoint lego \
  ljgonzalez/lego-cloudflare-certgen:3.3 --version

docker run --rm \
  -v "$PWD/template.domains.yaml:/app/domains.yaml:ro" \
  ljgonzalez/lego-cloudflare-certgen:3.3 --list
```

Inspect the size of the local image in MiB:

```bash
docker image inspect --format '{{.Size}}' ljgonzalez/lego-cloudflare-certgen:3.3 |
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
| `LEGO_DISABLE_CNAME_SUPPORT` | `true` | Set to `false` to follow an explicit DNS-01 CNAME delegation |
| `DOMAINS_FILE` | `./domains.yaml` | Host YAML path for Compose |
| `CERTGEN_IMAGE` | `ljgonzalez/lego-cloudflare-certgen:3.3` | Image used by Compose |
| `CERTGEN_ENV_FILE` | `./.env` | Settings file used by Compose |

### DNS-01 and wildcard names

A, AAAA and CNAME service records can coexist in a certificate group. DNS-01 validates control through a temporary TXT record under `_acme-challenge`. The default exact TXT owner handles CNAMEs inherited from wildcard service records. For an explicit CNAME delegation at `_acme-challenge`, authorize the destination Cloudflare zone and set `LEGO_DISABLE_CNAME_SUPPORT=false`. [Let's Encrypt DNS-01](https://letsencrypt.org/docs/challenge-types/#dns-01-challenge) · [Cloudflare wildcard records](https://developers.cloudflare.com/dns/manage-dns-records/reference/wildcard-dns-records/)

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

Run the offline regression suite with Bash, Python 3 and PyYAML:

```bash
python3 -B -m unittest discover -s tests -v
```

For an issuance check, use an active Cloudflare zone with an A record, an AAAA record, a CNAME and a wildcard CNAME. Put the four names in one group called `dns-smoke` in `dns-smoke.yaml` and run:

```bash
DOMAINS_FILE=./dns-smoke.yaml docker compose run --rm certgen --staging --only dns-smoke
```

A successful order reports **4 Attempted**, **4 Issued** and **0 Failed**. Use `openssl x509 -in /path/to/certificate.crt -noout -ext subjectAltName` to inspect the SANs.

## 📋 Issuance report

The ANSI table counts attempted, issued, failed and skipped DNS names per certificate group. Each certificate covers the full group. A failed order counts every attempted name in that group as failed, and the command exits with a nonzero status after printing the summary.

## :page_facing_up: License

[MIT](LICENSE) · [lego license](https://github.com/go-acme/lego/blob/master/LICENSE)