# lego-cloudflare-certgen

![lego-cloudflare-certgen](https://raw.githubusercontent.com/ljgonzalez1/lego-cloudflare-ssl-certgen/master/assets/readme-banner.png)

Issue Let's Encrypt certificates using lego and the Cloudflare DNS-01 challenge. A YAML group defines the names for one certificate. The container processes the groups once and exits.

## Supported platforms

| Image platform | Architecture |
|---|---|
| `linux/arm/v6` | ARMv6 |
| `linux/arm/v7` | ARMv7 |
| `linux/arm64` | ARM64 |
| `linux/386` | x86 (32-bit) |
| `linux/amd64` | x86-64 |

## Run with Docker Compose

Clone the [GitHub repository](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen), copy `example.env` to `.env`, and create `domains.yaml` using `template.domains.yaml` as a reference. Configure your ACME email and a Cloudflare API token with DNS write access to the zones you use. Read the Let's Encrypt terms of service and set `ACCEPT_LEGO_TOS=true` before issuing certificates.

```bash
docker compose run --rm certgen --list
docker compose run --rm certgen --staging --dry-run
docker compose run --rm certgen --staging
```

`--staging --dry-run` shows the planned operations. `--staging` requests staging certificates from Let's Encrypt. Certificates and ACME account data are written to `./ssl-certs`. Use `--production` when you are ready to request production certificates.

## Image tags and source

`latest` tracks the most recently published image. Pin a version tag for deployments that should stay on a specific release. [GitHub releases](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases) document each version.
