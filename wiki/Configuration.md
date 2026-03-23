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
| `DOCKER_SOCK` | No | `/var/run/docker.sock` | Path to the Docker socket (set for rootless Docker) |
| `BRIDGE_TYPES` | No | `reflector` | Comma-separated list of enabled bridge types (e.g. `reflector,xlx,zello`) |
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

![Reflector Config](images/admin-reflector.png)

Users with the **reflector admin** role can configure the GeuReflector/SVXReflector from `/admin/reflector`. This web UI edits the reflector's configuration file directly and provides sections for:

- **Global settings** — listen port, HTTP port, codecs, callsign accept/reject filters, timeouts, PKI paths, random QSY range
- **Clustering / Trunking** (GeuReflector) — LOCAL_PREFIX, CLUSTER_TGS, satellite mode configuration, satellite server settings, and trunk peer management (HOST, PORT, SECRET, REMOTE_PREFIX per peer)
- **Certificates** — ROOT_CA, ISSUING_CA, and SERVER_CERT sections (common name, org, locality, country, etc.)
- **Users** — callsign-to-password-group mappings
- **Passwords** — password group definitions
- **Talkgroup rules** — per-TG allow patterns (regex), allow monitor patterns, auto QSY timeout, and activity visibility

All panels are collapsed by default. Each section includes inline help buttons linking to the official `svxreflector.conf(5)` documentation. Delete actions require confirmation via a styled modal dialog.

After saving, the dashboard automatically restarts the SVXReflector Docker container (via the Docker socket) so changes take effect immediately.

The reflector edit page is organized into three tabs:

1. **Configuration** — all the config panels listed above
2. **Node Block** — temporarily block nodes from transmitting (see below)
3. **Certificate Files** — view pending CSRs, issued certificates, export CA bundle, and reset PKI

### Node Block

Node Block lets a reflector admin temporarily prevent a node from transmitting. The blocked node can still listen but cannot talk. The block timer resets each time the node attempts to transmit.

It is accessible from two places:

- **Reflector admin page** (`/admin/reflector` → Node Block tab) — enter any callsign and select a duration
- **Dashboard** (`/`) — click the mute icon on a node card (visible to admins only)

Available durations: 1 minute, 5 minutes, 10 minutes, 30 minutes, 1 hour. Set to 0 to unblock.

Technically, the block works by writing a `NODE BLOCK <callsign> <seconds>` command to the reflector's control pipe (`/dev/shm/reflector_ctrl`) inside the Docker container. The block is runtime-only and does not persist across reflector restarts.

### PKI certificates

The Certificates panel on the reflector edit page is split into a separate **Certificate Files** tab. When PKI certificates already exist (detected by checking for `.crt` files in the `reflector_pki` volume), the certificates form is **locked** to prevent accidental changes. To unlock it, use the **Reset PKI** action from the Certificate Files tab, which deletes all existing certificates and allows reconfiguration.

The `reflector_pki` volume is mounted read-only into the web container at `/rails/reflector_pki` for detection purposes.

When certificates exist, the certificate form fields are disabled in the UI. The controller preserves existing certificate config sections (ROOT_CA, ISSUING_CA, SERVER_CERT) when saving, so they are not silently dropped from the configuration file.

## Bridge configuration

SVXLink bridges are managed from `/admin/bridges`. Each bridge generates its own set of config files and runs as a separate Docker container. See the [[Bridges]] wiki page for full details on bridge types, config generation, backups, and archiving.
