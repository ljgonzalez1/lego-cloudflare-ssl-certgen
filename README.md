# :closed_lock_with_key: lego-cloudflare-certgen

> One-shot Docker container for SSL/TLS certificate generation via **Let's Encrypt**,
> [lego](https://github.com/go-acme/lego) **v5.2.1**, and the **Cloudflare DNS-01**
> challenge. No HTTP ports or web server are required.

---

![Readme Banner Image](assets/readme-banner.png)

---

## What this image does

This container runs `lego` once, requests one certificate covering every domain
listed in `/domains.txt`, writes the resulting files under `/ssl-certs/<timestamp>/`,
and exits.

The domain list is always expected at:
```text
/domains.txt
```
or custom file. When running the container locally, mount your host file as `/domains.txt:ro`.

---

## Requirements

- Docker Engine
- A Cloudflare DNS API token with permission to edit DNS records for the relevant zones
- A `domains.txt` file with the names that must be included in the certificate

---

## Image

Published image:
```text
ljgonzalez/lego-cloudflare-certgen:lego5.2.1-v2.0
```

Build locally only if you need to rebuild or modify the image:

```bash
docker build -t ljgonzalez/lego-cloudflare-certgen:latest .
```

---

## Quick start with `docker run`

### 1. Create the environment file

```bash
cp template.env certgen.env
```

Edit `certgen.env`. At minimum, set:

```env
EMAIL=you@example.com
ACCEPT_LEGO_TOS=true
PRODUCTION=false
```

Keep `PRODUCTION=false` for tests. Set `PRODUCTION=true` only when you are ready
to request real browser-trusted certificates.

### 2. Create `./domains.txt`

```text
example.com
*.example.com
*.sub.example.com
example.io
```

The file is mounted read-only into the container as `/domains.txt`.

### 3. Create the output directory

```bash
mkdir -p ./ssl-certs
```

### 4. Export the Cloudflare token in your shell

```bash
export CLOUDFLARE_API_KEY="your-cloudflare-api-token"
```

Do not store the token in `certgen.env` unless you intentionally accept that risk.
With `docker run`, environment variables are visible through Docker container
metadata while the container exists. Use `--rm` so the stopped container is removed.

### 5. Run

```bash
docker run --rm \
  --env-file ./certgen.env \
  --env CLOUDFLARE_API_KEY="${CLOUDFLARE_API_KEY}" \
  --volume "$(pwd)/ssl-certs:/ssl-certs" \
  --volume "$(pwd)/domains.txt:/domains.txt:ro" \
  --security-opt no-new-privileges:true \
  --cap-drop ALL \
  --cap-add CHOWN \
  --cap-add SETUID \
  --cap-add SETGID \
  ljgonzalez/lego-cloudflare-certgen:lego5.2.1-v2.0
```

---

## Output

Certificates are written to a timestamped directory:

```text
ssl-certs/
└── 2026-04-20_17.18.00_GMT-3/
    ├── accounts/
    │   └── acme-v02.api.letsencrypt.org/
    │       └── you@example.com/
    └── certificates/
        ├── example.com.crt
        ├── example.com.issuer.crt
        ├── example.com.json
        └── example.com.key
```

The exact certificate filename is chosen by `lego`, usually based on the first
domain in `domains.txt`.

---

## Configuration reference

Variables are read from `certgen.env`, except for the Cloudflare token when you
pass it separately with `--env CLOUDFLARE_API_KEY=...`.

| Variable | Default | Required | Description |
|---|---:|:---:|---|
| `TZ` | `Etc/UTC` | No | IANA timezone used for timestamps and output directory names |
| `EMAIL` | - | Yes | Let's Encrypt account e-mail for expiry notifications |
| `PRODUCTION` | `false` | No | `false` uses Let's Encrypt staging; `true` issues real trusted certificates |
| `CLOUDFLARE_API_KEY` | - | Yes | Cloudflare DNS API token. Prefer passing it via `--env` or Docker secret |
| `PROPAGATION_SECONDS` | `60` | No | Seconds to wait for DNS TXT record propagation |
| `DNS_RESOLVERS` | `1.1.1.1:53,8.8.8.8:53,1.0.0.1:53` | No | Resolvers used by lego to verify TXT record visibility |
| `ACCEPT_LEGO_TOS` | `false` | Yes | Must be `true` to accept the Let's Encrypt Terms of Service |
| `UID` | `1000` | No | Host UID that should own the generated certificate files |
| `GID` | `1000` | No | Host GID that should own the generated certificate files |

---

## Cloudflare API token

Create a Cloudflare API token with the minimum permissions needed for DNS-01:

| Resource | Permission |
|---|---|
| Zone - DNS | Edit |
| Zone - Zone | Read |

Scope the token only to the zones you need. For example, if `domains.txt`
contains `example.com` and `example.io`, the token should not have access to
unrelated zones.

---

## Security notes

The suggested `docker run` drops every Linux capability and adds back only the
capabilities required by the entrypoint:

| Capability | Why it is needed |
|---|---|
| `CHOWN` | The root phase sets ownership of `/ssl-certs` and the timestamped output directory |
| `SETUID` | `gosu` drops from root to the non-root `certgen` user |
| `SETGID` | `gosu` drops to the configured group |

`--security-opt no-new-privileges:true` prevents the process from gaining new
privileges through SUID bits or file capabilities.

The token is more exposed when passed with `--env` than when passed as a Docker
secret. For one-shot `docker run`, always keep `--rm` so the stopped container and
its environment are removed when the run ends.

---

## Project files

Required files
```text
.
├── ssl-certs/
├── compose.yaml             # if you are going to use Docker Compose
├── ssl-certs.env
├── .env                     # if you are going to use Docker Compose
└── domains.txt              # runtime domain list mounted as /domains.txt:ro
```

If using `docker compose`, the `.env` file must contain your Cloudflare API key.

---

## Notes

- Staging is the default. `PRODUCTION=false` uses Let's Encrypt staging, which is
  suitable for testing but not trusted by browsers.
- One run creates one certificate, which may contain multiple SAN entries from
  `domains.txt`.
- Re-run the container to renew or regenerate certificates.
- Production issuance is rate-limited. Test with staging first.

---

## Licence

This project is a wrapper around [lego](https://github.com/go-acme/lego), which is
licensed under the MIT Licence. Refer to the lego repository for its terms.
