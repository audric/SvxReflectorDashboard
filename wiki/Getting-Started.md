# Getting Started

## Requirements

- [Docker Engine](https://docs.docker.com/engine/install/)
- [Docker Compose v2](https://docs.docker.com/compose/install/) (`docker compose` subcommand)

## 1. Clone the repository

```bash
git clone https://github.com/audric/SvxReflectorDashboard
cd rails
```

## 2. Configure environment variables

```bash
cp .env.example .env
```

Edit `.env` and set the required values. See [[Configuration]] for the full list. At minimum you need:

```
DOMAIN=dashboard.example.com
BRAND_NAME=MyReflector
REFLECTOR_STATUS_URL=http://your-reflector-ip:8080/status
SECRET_KEY_BASE=<run: openssl rand -hex 64>
REFLECTOR_HOST=your-reflector-ip
```

Set `DOMAIN` to your public hostname for automatic HTTPS, or leave as `localhost` for local development.

## 3. Pull and start services

Pre-built multi-arch images (amd64, arm64/Raspberry Pi) are available from GitHub Container Registry:

```bash
docker compose pull
docker compose up -d
```

This starts seven services: `svxreflector`, `caddy`, `web`, `updater`, `audio_bridge`, `mqtt`, and `redis`.

To build images locally instead (e.g. for development or custom changes):

```bash
docker compose build
docker compose up -d
```

## 4. Initialize the database

```bash
docker compose run --rm web ./bin/rails db:prepare
```

This creates the database, runs migrations, and seeds a default admin account:

| Callsign | Password   |
| -------- | ---------- |
| `ADM1N`  | `changeme` |

**Change this password immediately** after first login at `/login`.

## 5. Access the dashboard

Open <https://yourdomain.com> (or <http://localhost> for local dev) in your browser.

## Useful commands

```bash
# Tail logs for all services
docker compose logs -f

# Tail individual service logs
docker compose logs -f web
docker compose logs -f updater
docker compose logs -f audio_bridge

# Open a Rails console
docker compose exec web bin/rails console

# Rebuild the audio bridge after Go code changes
docker compose build audio_bridge && docker compose up -d audio_bridge

# Stop containers (keep volumes)
docker compose down

# Stop and delete volumes (removes the database)
docker compose down -v
```

## Deploying to production

The `Dockerfile` uses a multi-stage build producing a minimal, non-root production image. The audio bridge has its own `Dockerfile` in `audio_bridge/`.

Pre-built images for **amd64** and **arm64** (Raspberry Pi 3/4/5) are automatically built and pushed to GitHub Container Registry on every push to `main`:

| Image | Registry |
|---|---|
| Web + Updater | `ghcr.io/audric/svxreflectordashboard-web` |
| Audio Bridge | `ghcr.io/audric/svxreflectordashboard-audio-bridge` |

### Production checklist

- Set `SECRET_KEY_BASE` to a securely generated value
- Point `BRAND_NAME` and `REFLECTOR_STATUS_URL` at your live reflector
- Set `REFLECTOR_HOST` for audio bridge connectivity
- Mount a persistent volume for `/rails/storage` (SQLite database)
- Set `DOMAIN` to your public hostname — Caddy handles TLS automatically via Let's Encrypt
- Ensure DNS points to your VPS before starting (Caddy needs to reach Let's Encrypt)
- **TLS is required** for browser PTT (microphone access needs a secure context)
