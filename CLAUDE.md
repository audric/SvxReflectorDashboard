# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

SVX Reflector Dashboard — a Rails 7.1 app (Ruby 3.2.10) for monitoring amateur radio SVXReflector node activity in real time. It polls a reflector's HTTP status API, persists node events in SQLite, and pushes live updates to browsers via ActionCable/Redis.

## Development Commands

### Docker (primary workflow)
```bash
docker compose up --build          # Build and start all services
docker compose up -d               # Start in background
docker compose down                # Stop all services
docker compose logs -f web         # Follow web server logs
docker compose logs -f updater     # Follow poller/updater logs
```

### Local Rails (without Docker)
```bash
bin/setup                          # Install deps, prepare DB
bin/rails server                   # Start Puma on port 3000
bin/rails db:prepare               # Create/migrate database
bin/rails assets:precompile        # Precompile assets
bin/rails console                  # Interactive Rails console
```

### Running the background poller locally
```bash
bin/rails runner 'require Rails.root.join("lib","reflector_listener").to_s; ReflectorListener.start; sleep'
```

## Required Environment Variables

See `.env.example`. Copy to `.env` and set:
- `BRAND_NAME` — display name for the reflector
- `REFLECTOR_STATUS_URL` — the SVXReflector HTTP status endpoint (e.g. `http://host:8080/status`)
- `SECRET_KEY_BASE` — generate with `openssl rand -hex 64`

Optional: `REDIS_URL` (defaults to `redis://redis:6379/1` in Docker), `DOMAIN` (your public hostname — Caddy uses it for TLS and Rails derives ActionCable allowed origins from it)

## Architecture

### Three-service Docker setup
- **web** — Puma serving the Rails app on port 3000
- **updater** — runs `ReflectorListener` as a background Rails runner process
- **redis** — ActionCable pub/sub adapter

The web and updater services share a Docker volume (`db_data` → `/rails/storage`) for the SQLite database.

### Core data flow
1. `ReflectorListener` (lib/reflector_listener.rb) polls `REFLECTOR_STATUS_URL` every 4 seconds
2. Diffs node state changes (tg, isTalker, RX squelch) against previous snapshot
3. Broadcasts changed nodes via `ActionCable.server.broadcast('updates', payload)`
4. Persists events to `node_events` table (6 event types: `talking_start`, `talking_stop`, `tg_join`, `tg_leave`, `connected`, `disconnected`)

### Single controller, single model
- **DashboardController** — 4 actions, each calls `fetch_nodes` which does a live HTTP GET to the reflector status API
  - `index` (root `/`) — live node grid with WebSocket updates
  - `map` (`/map`) — Leaflet.js interactive map
  - `stats` (`/stats`) — historical analytics from NodeEvent + live snapshot counts, filterable by period
  - `tg` (`/tg`) — CTCSS tone-to-talkgroup matrix
- **NodeEvent** — single model with scopes: `talks`, `tg_joins`, `by_period(day|month|year|all)`
- **UpdatesChannel** — streams from `'updates'` channel for WebSocket broadcasts

### Frontend
- HAML templates (not ERB)
- Hotwire (Turbo + Stimulus) for interactivity
- Tailwind CSS (Play CDN runtime, not compiled)
- Bootstrap Icons for iconography (no Bootstrap CSS framework)
- Leaflet.js for the map view
- No separate JavaScript build pipeline — uses Sprockets asset pipeline

### Database
SQLite with a single `node_events` table. Indexed on `callsign`, `event_type`, `created_at`, `tg`. Database file lives in `/rails/storage` (persisted via Docker volume).

## Notes
- No test suite is configured
- No linting/formatting tools are configured
- Templates use HAML — do not create ERB templates
