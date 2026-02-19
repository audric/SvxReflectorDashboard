# SVX Dashboard

A Rails web application for monitoring and visualizing amateur radio reflector node activity in real time. It connects to an [SVXReflector](https://www.svxlink.org/) instance, stores node events in a database, and exposes a live dashboard with multiple views.

## Features

- **Live dashboard** — node grid with color-coded status (Talking / Active / Idle), signal levels, squelch indicators, and a scrolling activity log updated via WebSocket
- **Map** — interactive Leaflet.js map with per-node popups, multiple tile layers (Dark / Street / Satellite / Topo), and auto-zoom
- **Stats** — historical analytics filterable by period (Today / Month / Year / All-time): top talkers, top talkgroups, node type distribution, signal strength, and recent event log
- **TG Matrix** — CTCSS tone-to-talkgroup mapping table across all nodes
- **Background updater** — polls the reflector status API every 4 seconds, detects state changes, broadcasts diffs over ActionCable, and persists events to the database

## Architecture

```
web      → Rails app (Puma), serves the dashboard on port 3000
updater  → Background process running ReflectorListener
redis    → ActionCable adapter for WebSocket broadcasts
db_data  → Named Docker volume persisting the SQLite database (storage/)
```

**Stack:** Ruby 3.2 · Rails 7.1 · SQLite · Redis · Hotwire (Turbo + Stimulus) · HAML · Bootstrap 5 · Leaflet.js

## Requirements

- [Docker Engine](https://docs.docker.com/engine/install/)
- [Docker Compose v2](https://docs.docker.com/compose/install/) (`docker compose` subcommand)

## Getting started

### 1. Clone the repository

```bash
git clone <your-repo-url>
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

This starts `web`, `updater`, and `redis`. The dashboard is available at <http://localhost:3000>.

### 4. Initialize the database

```bash
docker compose run --rm web ./bin/rails db:create db:migrate
```

> If you see permission errors on `storage/` files, recreate the volume:
> `docker compose down -v` (data will be lost) then re-run migrations.

## Environment variables

| Variable               | Description                                                       |
| ---------------------- | ----------------------------------------------------------------- |
| `BRAND_NAME`           | Hostname of the SVXReflector                                      |
| `REFLECTOR_STATUS_URL` | HTTP status API polled by the updater                             |
| `REDIS_URL`            | Redis connection URL (default in compose: `redis://redis:6379/1`) |
| `SECRET_KEY_BASE`      | Rails secret key — required, see below                            |
| `RAILS_ENV`            | Rails environment (default in container: `production`)            |
| `RAILS_MAX_THREADS`    | ActiveRecord connection pool size (default: `5`)                  |

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

## Useful commands

```bash
# Tail logs for all services
docker compose logs -f

# Tail only the web or updater logs
docker compose logs -f web
docker compose logs -f updater

# Open a Rails console
docker compose run --rm web ./bin/rails console

# Run an arbitrary rake task
docker compose run --rm web ./bin/rails <task>

# Stop containers (keep volumes)
docker compose down

# Stop containers and delete volumes (removes the database)
docker compose down -v
```

## Deploying to production

The `Dockerfile` uses a multi-stage build and produces a minimal, non-root production image.

### Build and push the image

```bash
docker build -t yourorg/svx:latest .
docker push yourorg/svx:latest
```

### Run on the target host

Use Docker Compose (or your preferred orchestrator) with production-safe values for all environment variables. Minimum checklist:

- Set `SECRET_KEY_BASE` to a securely generated value (`openssl rand -hex 64`)
- Point `BRAND_NAME` and `REFLECTOR_STATUS_URL` at your live reflector
- Mount or bind a persistent volume for `/rails/storage` (SQLite database)
- Expose port `3000` behind a reverse proxy (nginx, Caddy, Traefik…) with TLS

### Publish to GitHub

```bash
git remote add origin git@github.com:<your-org>/<repo>.git
git branch -M main
git push -u origin main
```

## Troubleshooting

| Symptom                       | Fix                                                                          |
| ----------------------------- | ---------------------------------------------------------------------------- |
| Port 3000 already in use      | Change the host port in `docker-compose.yml` or stop the conflicting service |
| Database permission errors    | `docker compose down -v` then re-run `db:create db:migrate`                  |
| Dashboard shows no nodes      | Verify `REFLECTOR_STATUS_URL` is reachable from inside the container         |
| WebSocket updates not working | Check that Redis is running (`docker compose ps`) and `REDIS_URL` is correct |

## Code map

| Path                                      | Purpose                                      |
| ----------------------------------------- | -------------------------------------------- |
| `lib/reflector_listener.rb`               | Background polling and event broadcasting    |
| `app/models/node_event.rb`                | ActiveRecord model for persisted events      |
| `app/channels/updates_channel.rb`         | ActionCable channel for live UI updates      |
| `app/controllers/dashboard_controller.rb` | Dashboard, map, stats, and TG matrix actions |
| `app/views/dashboard/`                    | HAML templates for all views                 |
| `Dockerfile`                              | Multi-stage production image                 |
| `docker-compose.yml`                      | Local/production service definitions         |

## Author

Developed by **IW1GEU**, member of the [XLX585](https://xlx585.net) group.
