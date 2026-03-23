# SVX Dashboard

A Rails web application for monitoring amateur radio [SVXReflector](https://www.svxlink.org/) / [GeuReflector](https://github.com/audric/geureflector) node activity in real time. Supports GeuReflector extensions: trunk links, cluster TGs, and satellites. Registered users can tune in to talkgroups and transmit audio directly from their browser.

**[Wiki](https://github.com/audric/SvxReflectorDashboard/wiki)** — full documentation

## Features

- Live node grid with signal levels, squelch indicators, and activity log
- Interactive map with per-node popups and multiple tile layers
- Historical stats: top talkers, top talkgroups, signal strength
- Web listener — tune in to any talkgroup (Opus audio via WebSocket)
- S-meter and spectrum analyser showing real-time audio levels (RX and TX)
- Push-to-Talk from the browser (requires HTTPS)
- CTCSS tone-to-talkgroup matrix with CHIRP CSV export
- GeuReflector support — trunk link status, satellite monitoring, cluster TG indicators, and network-wide analytics
- Web admin for SVXReflector/GeuReflector configuration (global settings, certificates, users, passwords, TG rules, trunk peers, satellites)
- Multi-protocol bridge management — SVXLink reflector-to-reflector, EchoLink, XLX (DCS/DExtra), DMR, YSF, AllStar, and Zello bridges with auto-generated configs, snapshot backups, and 30-day archive on delete
- User management with callsign validation, admin approval, and role-based permissions

## Quick start

```bash
git clone https://github.com/audric/SvxReflectorDashboard
cd rails
cp .env.example .env   # edit with your reflector details
docker compose pull     # pre-built images for amd64 and arm64
docker compose up -d
docker compose run --rm web ./bin/rails db:prepare
```

Pre-built multi-arch images (amd64, arm64/Raspberry Pi) are published to `ghcr.io/audric/svxreflectordashboard-web`. To build locally instead, run `docker compose build`.

Set `DOMAIN=yourdomain.com` in `.env` for automatic HTTPS, or leave as `localhost` for local dev.

Open <https://yourdomain.com> (or <http://localhost> locally). Default admin: `ADM1N` / `changeme`.

See the wiki for [configuration details](https://github.com/audric/SvxReflectorDashboard/wiki/Configuration) and [production deployment](https://github.com/audric/SvxReflectorDashboard/wiki/Getting-Started#deploying-to-production).

## Architecture

```
svxreflector  → GeuReflector daemon (SVXReflector-compatible, with trunks/satellites)
caddy         → Reverse proxy with automatic HTTPS (Let's Encrypt)
web           → Rails app (Puma) on port 3000
updater       → Background poller (ReflectorListener)
audio_bridge  → Go binary, SVXReflector protocol V2
xlx_bridge    → Go binary, D-STAR XLX bridge (DCS + DExtra protocols)
zello_bridge  → Go binary, Zello channel bridge (WebSocket/OPUS)
redis         → ActionCable + audio pub/sub
```

**Stack:** Ruby 3.2 · Rails 8.0 · Go · SQLite · Redis · HAML · Tailwind CSS · Leaflet.js

See the wiki for [architecture details](https://github.com/audric/SvxReflectorDashboard/wiki/Architecture) and [reflector protocol docs](https://github.com/audric/SvxReflectorDashboard/wiki/Reflector-Protocol).

## Documentation

| Topic | Link |
|---|---|
| Getting Started | [wiki/Getting-Started](https://github.com/audric/SvxReflectorDashboard/wiki/Getting-Started) |
| Architecture | [wiki/Architecture](https://github.com/audric/SvxReflectorDashboard/wiki/Architecture) |
| Configuration | [wiki/Configuration](https://github.com/audric/SvxReflectorDashboard/wiki/Configuration) |
| User Management | [wiki/User-Management](https://github.com/audric/SvxReflectorDashboard/wiki/User-Management) |
| Bridges | [wiki/Bridges](https://github.com/audric/SvxReflectorDashboard/wiki/Bridges) |
| Audio Bridge | [wiki/Audio-Bridge](https://github.com/audric/SvxReflectorDashboard/wiki/Audio-Bridge) |
| Reflector Protocol | [wiki/Reflector-Protocol](https://github.com/audric/SvxReflectorDashboard/wiki/Reflector-Protocol) |
| Troubleshooting | [wiki/Troubleshooting](https://github.com/audric/SvxReflectorDashboard/wiki/Troubleshooting) |
| Code Map | [wiki/Code-Map](https://github.com/audric/SvxReflectorDashboard/wiki/Code-Map) |

## Author

Developed by **IW1GEU**, member of the [XLX585](https://xlx585.net) group.
