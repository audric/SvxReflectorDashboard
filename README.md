# SVX Dashboard

A Rails web application for monitoring and visualizing amateur radio reflector node activity in real time. It connects to an [SVXReflector](https://www.svxlink.org/) instance, stores node events in a database, and exposes a live dashboard with multiple views. Registered users can tune in to talkgroups and transmit audio directly from their browser.

## Features

- **Live dashboard** — node grid with color-coded status (Talking / Active / Idle), signal levels, squelch indicators, and a scrolling activity log updated via WebSocket
- **Map** — interactive Leaflet.js map with per-node popups, multiple tile layers (Dark / Street / Satellite / Topo), and auto-zoom
- **Stats** — historical analytics filterable by period (Today / Month / Year / All-time): top talkers, top talkgroups, node type distribution, signal strength, and recent event log
- **TG Matrix** — CTCSS tone-to-talkgroup mapping table across all nodes
- **Web listener** — tune in to any talkgroup and receive live audio in the browser via Opus/WebSocket
- **Push-to-Talk** — transmit audio from the browser microphone (requires HTTPS and a connected mic)
- **User accounts** — registration with callsign validation, admin approval workflow, per-user monitor/transmit permissions
- **Admin panel** — user management, approve/reject registrations, reflector settings
- **Background updater** — polls the reflector status API every 4 seconds, detects state changes, broadcasts diffs over ActionCable, and persists events to the database

## Architecture

```
web           → Rails app (Puma), serves the dashboard on port 3000
updater       → Background process running ReflectorListener
audio_bridge  → Go binary, bridges browser audio ↔ reflector (SVXReflector protocol V2)
redis         → ActionCable adapter + audio pub/sub + web node metadata cache
db_data       → Named Docker volume persisting the SQLite database (storage/)
```

### Data flow

1. `ReflectorListener` (lib/reflector_listener.rb) polls `REFLECTOR_STATUS_URL` every 4 s
2. Diffs node state changes (tg, isTalker, RX squelch) against previous snapshot
3. Enriches web listener nodes with browser/location metadata from Redis
4. Broadcasts changed nodes via `ActionCable.server.broadcast('updates', payload)`
5. Persists events to `node_events` table (6 event types: `talking_start`, `talking_stop`, `tg_join`, `tg_leave`, `connected`, `disconnected`)
6. Caches the latest snapshot in Redis so page loads never block on HTTP

### Audio path

```
Browser mic → MediaStreamTrackProcessor → Opus encoder
  → ActionCable (audio:tx) → Redis → audio_bridge (Go)
  → SVXReflector UDP (protocol V2)

SVXReflector UDP → audio_bridge → Redis (audio:tg:<N>)
  → ActionCable → Browser → Opus decoder → AudioContext → speaker
```

**Stack:** Ruby 3.2 · Rails 7.1 · Go · SQLite · Redis · Hotwire (Turbo + Stimulus) · HAML · Bootstrap 5 · Leaflet.js

## Requirements

- [Docker Engine](https://docs.docker.com/engine/install/)
- [Docker Compose v2](https://docs.docker.com/compose/install/) (`docker compose` subcommand)

## Getting started

### 1. Clone the repository

```bash
git clone https://github.com/audric/SvxReflectorDashboard
cd rails
```

### 2. Configure environment variables

Copy the example file and fill in your values:

```bash
cp .env.example .env
```

Docker Compose automatically loads `.env` from the project root. See [Environment variables](#environment-variables) below for the full list.

### 3. Build and start services

```bash
docker compose build
docker compose up -d
```

This starts `web`, `updater`, `audio_bridge`, and `redis`. The dashboard is available at <http://localhost:3000>.

### 4. Initialize the database

```bash
docker compose run --rm web ./bin/rails db:prepare
```

This creates the database, runs migrations, and seeds a default admin account (`ADMIN` / `changeme`). Change the password immediately after first login.

> If you see permission errors on `storage/` files, recreate the volume:
> `docker compose down -v` (data will be lost) then re-run `db:prepare`.

## Environment variables

| Variable               | Required | Description                                                       |
| ---------------------- | -------- | ----------------------------------------------------------------- |
| `BRAND_NAME`           | Yes      | Display name for the reflector                                    |
| `REFLECTOR_STATUS_URL` | Yes      | HTTP status API polled by the updater (e.g. `http://host:8080/status`) |
| `SECRET_KEY_BASE`      | Yes      | Rails secret key — see below                                     |
| `REFLECTOR_HOST`       | Yes      | Reflector IP/hostname for the audio bridge                        |
| `REFLECTOR_PORT`       | No       | Reflector port for the audio bridge (default: `5300`)             |
| `REDIS_URL`            | No       | Redis connection URL (default in compose: `redis://redis:6379/1`) |
| `RAILS_ENV`            | No       | Rails environment (default in container: `production`)            |
| `ALLOWED_HOST`         | No       | Hostname for Rails host authorization                             |

### Generating SECRET_KEY_BASE

`SECRET_KEY_BASE` is a mandatory secret used by Rails to sign and encrypt session data. Generate one with:

```bash
openssl rand -hex 64
```

Copy the output and paste it into your `.env` file:

```
SECRET_KEY_BASE=paste_the_generated_value_here
```

Never reuse, share, or commit this value. Rotating it will invalidate all existing sessions.

## User management

On first boot, `db:seed` creates a default admin account:

| Callsign | Password   |
| -------- | ---------- |
| `ADMIN`  | `changeme` |

Log in at `/login` and change the password. From the admin panel (`/admin/users`) you can:

- Approve or reject new registrations
- Grant or revoke **monitor** (tune-in) and **transmit** (PTT) permissions per user
- Promote users to admin

New users register at `/register` with a valid amateur radio callsign and are held in a pending state until approved by an admin.

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

# Run an arbitrary rake task
docker compose exec web bin/rails <task>

# Rebuild the audio bridge after Go code changes
docker compose build audio_bridge && docker compose up -d audio_bridge

# Stop containers (keep volumes)
docker compose down

# Stop containers and delete volumes (removes the database)
docker compose down -v
```

## Deploying to production

The `Dockerfile` uses a multi-stage build and produces a minimal, non-root production image. The audio bridge has its own Dockerfile in `audio_bridge/`.

### Production checklist

- Set `SECRET_KEY_BASE` to a securely generated value (`openssl rand -hex 64`)
- Point `BRAND_NAME` and `REFLECTOR_STATUS_URL` at your live reflector
- Set `REFLECTOR_HOST` to the reflector's IP for audio bridge connectivity
- Mount or bind a persistent volume for `/rails/storage` (SQLite database)
- Expose port `3000` behind a reverse proxy (nginx, Caddy, Traefik…) with TLS
- **TLS is required** for browser PTT (microphone access needs a secure context)

## Troubleshooting

| Symptom                       | Fix                                                                          |
| ----------------------------- | ---------------------------------------------------------------------------- |
| Port 3000 already in use      | Change the host port in `docker-compose.yml` or stop the conflicting service |
| Database permission errors    | `docker compose down -v` then re-run `db:prepare`                            |
| Dashboard shows no nodes      | Verify `REFLECTOR_STATUS_URL` is reachable from inside the container         |
| WebSocket updates not working | Check that Redis is running (`docker compose ps`) and `REDIS_URL` is correct |
| No audio when tuning in       | Check `audio_bridge` logs; verify `REFLECTOR_HOST` is reachable              |
| PTT not available             | Requires HTTPS (secure context), a connected microphone, and transmit permission |
| Web listener not on map       | Geolocation must be permitted in the browser; marker appears after next poll cycle |

## Code map

| Path                                      | Purpose                                           |
| ----------------------------------------- | ------------------------------------------------- |
| `lib/reflector_listener.rb`               | Background polling, event broadcasting, node enrichment |
| `app/models/node_event.rb`                | ActiveRecord model for persisted events            |
| `app/models/user.rb`                      | User model with callsign validation and roles      |
| `app/channels/updates_channel.rb`         | ActionCable channel for live UI updates            |
| `app/channels/audio_channel.rb`           | ActionCable channel for audio streaming and PTT    |
| `app/controllers/dashboard_controller.rb` | Dashboard, map, stats, and TG matrix actions       |
| `app/controllers/sessions_controller.rb`  | Login / logout                                     |
| `app/controllers/registrations_controller.rb` | User registration                              |
| `app/controllers/admin/users_controller.rb` | Admin user management and approval               |
| `app/views/dashboard/`                    | HAML templates for all views                       |
| `audio_bridge/`                           | Go binary — reflector protocol V2 client           |
| `audio_bridge/main.go`                    | Session manager, Redis command listener            |
| `audio_bridge/client.go`                  | TCP/UDP reflector protocol implementation          |
| `audio_bridge/protocol.go`               | Wire format builders and parsers                   |
| `Dockerfile`                              | Multi-stage production image (Rails)               |
| `audio_bridge/Dockerfile`                 | Production image (Go audio bridge)                 |
| `docker-compose.yml`                      | Service definitions (web, updater, audio_bridge, redis) |

## Author

Developed by **IW1GEU**, member of the [XLX585](https://xlx585.net) group.
