# 🔐 Lego Cloudflare Certgen

![Project banner](https://raw.githubusercontent.com/ljgonzalez1/lego-cloudflare-ssl-certgen/master/assets/readme-banner.png)

[GitHub repository and documentation](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen)

Issue Let's Encrypt certificates with **lego** and **Cloudflare DNS-01**. Each group in `domains.yaml` produces one certificate for ordinary names and wildcards. Names with A, AAAA, or CNAME records use the same challenge. The container prints a certificate summary and exits.

> ⚠️ **Start in staging.** Repeated production runs can hit Let's Encrypt's rate limit for the same set of names. Use `--staging` to test and switch to `--production` after issuance succeeds. [Let's Encrypt rate limits](https://letsencrypt.org/docs/rate-limits/)

## 🚀 Quick start with Docker

### 1. Get the templates

Run these commands in a new working directory.

```bash
mkdir lego-certgen
cd lego-certgen
curl -fL -o domains.yaml https://raw.githubusercontent.com/ljgonzalez1/lego-cloudflare-ssl-certgen/master/template.domains.yaml
curl -fL -o .env https://raw.githubusercontent.com/ljgonzalez1/lego-cloudflare-ssl-certgen/master/example.env
chmod 600 .env
mkdir -p ssl-certs
```

### 2. Set your token and names

Create a Cloudflare token with **Zone DNS Edit** and **Zone Read** for the zones you use. Fill in `EMAIL` and `CLOUDFLARE_API_TOKEN` in `.env`. Set `ACCEPT_LEGO_TOS=true` after reviewing the [Let's Encrypt Terms of Service](https://letsencrypt.org/repository/).

Replace the contents of `domains.yaml` with your own names. Keep wildcards quoted.

```yaml
website:
  - example.com
  - www.example.com
  - "*.example.com"
```

### 3. Run the container

```bash
docker run --rm -t --env-file .env \
  -v "$PWD/domains.yaml:/app/domains.yaml:ro" \
  -v "$PWD/ssl-certs:/ssl-certs" \
  ljgonzalez/lego-cloudflare-certgen:latest --staging
```

Add `--dry-run` to preview the request. When staging works, replace `--staging` with `--production` for trusted certificates. The files are saved under `ssl-certs/`. For a pinned deployment, replace `latest` with a version tag from the table below.

## 🏷️ Tags and platforms

The current image supports ARMv6, ARMv7, ARM64, i386, and AMD64. Docker selects the platform when it pulls the image.

<!-- BEGIN DOCKER TAG TABLE -->

| tag | runtime | lego | other aliases |
|---|---|---|---|
| [`latest`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.7), [`v4.2.7`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.7) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`v4`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.7), [`alpine3.24.4-lego4.34.0-v4.2.7`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.7) |
| [`v4.2.6`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.6) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`lego4.34-v4.2.6`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.6), [`lego4.34.0-v4.2.6`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.6), [`alpine3.24.4-lego4.34.0-v4.2.6`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.6) |
| [`v4.2.5`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.5) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`lego4.34-v4.2.5`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.5), [`lego4.34.0-v4.2.5`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.5), [`alpine3.24.4-lego4.34.0-v4.2.5`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.5) |
| [`v4.2.4`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.4) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`lego4.34-v4.2.4`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.4), [`lego4.34.0-v4.2.4`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.4), [`alpine3.24.4-lego4.34.0-v4.2.4`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.4) |
| [`v4.2.3`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.3) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`lego4.34-v4.2.3`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.3), [`lego4.34.0-v4.2.3`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.3), [`alpine3.24.4-lego4.34.0-v4.2.3`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.3) |
| [`v4.2.2`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.2) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`lego4.34-v4.2.2`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.2), [`lego4.34.0-v4.2.2`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.2), [`alpine3.24.4-lego4.34.0-v4.2.2`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.2) |
| [`v4.2.1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.1) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`lego4.34-v4.2.1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.1), [`lego4.34.0-v4.2.1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.1), [`alpine3.24.4-lego4.34.0-v4.2.1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v4.2.1) |

<!-- END DOCKER TAG TABLE -->

[Full tag history](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen#version-history) · [Docker Hub tags](https://hub.docker.com/r/ljgonzalez/lego-cloudflare-certgen/tags) · [MIT license](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/blob/master/LICENSE)
