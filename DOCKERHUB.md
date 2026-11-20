# 🔐 Lego Cloudflare Certgen

![Project banner](https://raw.githubusercontent.com/ljgonzalez1/lego-cloudflare-ssl-certgen/master/assets/readme-banner.png)

[GitHub repository and documentation](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen)

Issue Let's Encrypt certificates with **lego** and **Cloudflare DNS-01**. Each group in `domains.yaml` produces one certificate for ordinary names and wildcards. Names with A, AAAA, or CNAME records use the same challenge. The container prints a certificate summary and exits.

> ⚠️ **Start in staging.** Repeated production runs can hit Let's Encrypt's rate limit for the same set of names. Select **Staging** and confirm to try a live staging order. [Let's Encrypt rate limits](https://letsencrypt.org/docs/rate-limits/)

## 🚀 Quick start with Docker

### 1. Get the templates

Run these commands in a new working directory.

```bash
mkdir lego-certgen
cd lego-certgen
wget -O domains.yaml https://raw.githubusercontent.com/ljgonzalez1/lego-cloudflare-ssl-certgen/master/template.domains.yaml
wget -O .env https://raw.githubusercontent.com/ljgonzalez1/lego-cloudflare-ssl-certgen/master/template.env
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
docker run --rm -it --env-file .env \
  -v "$PWD/domains.yaml:/app/domains.yaml:ro" \
  -v "$PWD/ssl-certs:/ssl-certs" \
  ljgonzalez/lego-cloudflare-certgen:v6.0.0-alpha-2
```

Add `--dry-run` to preview the request. When staging works, choose **Production** and confirm it for trusted certificates. The files are saved under `ssl-certs/`. This command pins the Go beta; `latest` tracks stable releases in the table below.

❌ The live view reports definitive errors such as an invalid token, missing zone access or a rate limit. The failed group ends early and the other groups still run. Each certificate allows up to 100 names and has its own deadline of at most two hours. The run ends with the issued and failed counts.

🧪 **v6.0.0-alpha-2 migrates the application to Go**, with optimizations disabled, full debug symbols and redacted beta traces on errors. `latest` stays on stable **v5.2.4**. Delve is considered on AMD64, ARM64 and i386 and retained only after a real execution probe passes. All five architectures attempt Go logs and text dumps.

🤖 Add `--staging` to request staging certificates unattended. **`--production` bypasses confirmation and consumes live rate limits.** `--test` checks the compiled program and lego offline. The optional Compose file runs unattended in staging; edit its `x-certgen-mode` value to select production. [Debugging and full documentation](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen#debugging-the-go-beta)

📝 Every run attempts a private log under `/ssl-certs/debug`, preserved by the certificate bind above. Failures also produce `dump.<log-name>` before the supervisor closes. Names use `v6.0.0-alpha-2.AAAA-DD-MM_HH.MM.SS.arch.log`. The Go supervisor remains active when Delve is unsupported or unavailable. Abruptly killing the whole container cannot guarantee a final dump.

## 🏷️ Tags and platforms

The current image supports ARMv6, ARMv7, ARM64, i386, and AMD64. Docker selects the platform when it pulls the image.

`latest` tracks the last release outside the alpha, alfa and beta channels. Prerelease tags carry their suffix.

<!-- BEGIN DOCKER TAG TABLE -->

| tag | runtime | lego | other aliases |
|---|---|---|---|
| [`v6.0.1-beta-1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v6.0.1-beta-1) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`v6-beta-1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v6.0.1-beta-1), [`v6.0-beta-1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v6.0.1-beta-1), [`alpine3.24.4-lego4.34.0-v6.0.1-beta-1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v6.0.1-beta-1) |
| [`v6.0.0-alpha-1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v6.0.0-alpha-1) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`v6-alpha-1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v6.0.0-alpha-1), [`v6.0-alpha-1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v6.0.0-alpha-1), [`alpine3.24.4-lego4.34.0-v6.0.0-alpha-1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v6.0.0-alpha-1) |
| [`latest`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.4), [`v5.2.4`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.4) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`v5`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.4), [`alpine3.24.4-lego4.34.0-v5.2.4`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.4) |
| [`v5.2.3`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.3) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`lego4.34-v5.2.3`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.3), [`lego4.34.0-v5.2.3`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.3), [`alpine3.24.4-lego4.34.0-v5.2.3`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.3) |
| [`v5.2.2`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.2) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`lego4.34-v5.2.2`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.2), [`lego4.34.0-v5.2.2`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.2), [`alpine3.24.4-lego4.34.0-v5.2.2`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.2) |
| [`v5.2.1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.1) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`lego4.34-v5.2.1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.1), [`lego4.34.0-v5.2.1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.1), [`alpine3.24.4-lego4.34.0-v5.2.1`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.1) |
| [`v5.2.0`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.0) | [Alpine 3.24.4 minimal on `scratch`](https://hub.docker.com/_/alpine) | [`v4.34.0`](https://github.com/go-acme/lego/releases/tag/v4.34.0) | [`lego4.34-v5.2.0`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.0), [`lego4.34.0-v5.2.0`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.0), [`alpine3.24.4-lego4.34.0-v5.2.0`](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v5.2.0) |

<!-- END DOCKER TAG TABLE -->

[Full tag history](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen#version-history) · [Docker Hub tags](https://hub.docker.com/r/ljgonzalez/lego-cloudflare-certgen/tags) · [MIT license](https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/blob/master/LICENSE)

## :page_facing_up: License

The original project code is [MIT licensed](LICENSE). Third-party programs, modules, CA/timezone data and fonts retain their own terms. Exact image notices are collected under `/usr/share/licenses/` and checked against the retained payload. See [LICENSES.md](LICENSES.md) for MIT publication scope, original attributions, source availability and unresolved artwork/trademark grants.
