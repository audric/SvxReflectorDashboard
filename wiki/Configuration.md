# Configuration

All configuration is done via environment variables. Docker Compose loads `.env` from the project root automatically.

## Environment variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `DOMAIN` | Yes | `localhost` | Public hostname for Caddy TLS and ActionCable allowed origins |
| `BRAND_NAME` | Yes | — | Display name shown in the navbar and page titles |
| `REFLECTOR_STATUS_URL` | Yes | — | HTTP status API endpoint (e.g. `http://host:8080/status`) |
| `SECRET_KEY_BASE` | Yes | — | Rails secret for signing sessions and cookies |
| `REFLECTOR_HOST` | Yes | — | Reflector IP/hostname for the audio bridge TCP/UDP connection |
| `REFLECTOR_PORT` | No | `5300` | Reflector port for the audio bridge |
| `REDIS_URL` | No | `redis://redis:6379/1` | Redis connection URL |
| `RAILS_ENV` | No | `production` | Rails environment |

## Generating SECRET_KEY_BASE

This is a mandatory secret used by Rails to sign and encrypt session data:

```bash
openssl rand -hex 64
```

Paste the output into your `.env`:

```
SECRET_KEY_BASE=paste_the_generated_value_here
```

Never reuse, share, or commit this value. Rotating it invalidates all existing sessions.

## Admin settings

Some settings can be changed at runtime from the admin panel (`/admin/settings`):

- **Reflector status URL** — overrides `REFLECTOR_STATUS_URL`
- **Poll interval** — how often the updater fetches the reflector status (1–10 seconds, default 4)

These are stored in the `settings` table and take effect on the next poll cycle without restarting services.

## Reflector configuration

Users with the **reflector admin** role can configure the SVXReflector itself from `/admin/reflector`. This web UI edits the reflector's configuration file directly and provides sections for:

- **Global settings** — listen port, HTTP port, codecs, callsign accept/reject filters, timeouts, PKI paths, random QSY range
- **Certificates** — ROOT_CA, ISSUING_CA, and SERVER_CERT sections (common name, org, locality, country, etc.)
- **Users** — callsign-to-password-group mappings
- **Passwords** — password group definitions
- **Talkgroup rules** — per-TG allow patterns (regex), allow monitor patterns, auto QSY timeout, and activity visibility

All panels are collapsed by default. Each section includes inline help buttons linking to the official `svxreflector.conf(5)` documentation. Delete actions require confirmation via a styled modal dialog.

After saving, the dashboard automatically restarts the SVXReflector Docker container (via the Docker socket) so changes take effect immediately.
