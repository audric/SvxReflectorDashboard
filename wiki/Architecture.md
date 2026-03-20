# Architecture

## Services

```
svxreflector  → GeuReflector instance (SVXReflector-compatible), configurable via the web admin UI
caddy         → Reverse proxy with automatic HTTPS (Let's Encrypt)
web           → Rails app (Puma), serves the dashboard on port 3000
updater       → Background process running ReflectorListener
audio_bridge  → Go binary, bridges browser audio ↔ reflector (SVXReflector protocol V2)
redis         → ActionCable adapter + audio pub/sub + web node metadata cache
db_data       → Named Docker volume persisting the SQLite database (storage/)
caddy_data    → Named Docker volume persisting TLS certificates
```

### caddy

A lightweight reverse proxy ([Caddy 2](https://caddyserver.com/)) that terminates TLS on ports 80/443 and forwards traffic to the Rails app. It automatically provisions and renews Let's Encrypt certificates using the `DOMAIN` environment variable. TLS is required for browser PTT (microphone access needs a secure context).

### web

The main Rails application served by Puma. Handles HTTP requests, renders HAML templates, and manages ActionCable WebSocket connections. Runs in Docker with the `RAILS_ENV=production` environment.

### updater

A background Rails runner process that starts `ReflectorListener`. It polls the reflector's HTTP status API, diffs node state, persists events to SQLite, and broadcasts changes over ActionCable via Redis.

### audio_bridge

A standalone Go binary that speaks the SVXReflector protocol V2 (TCP + UDP). It listens for commands on Redis channels and bridges audio between browsers and the reflector. See [[Audio Bridge]] for details.

### svxreflector

The [GeuReflector](https://github.com/audric/geureflector) instance (100% SVXReflector-compatible) runs as a Docker container alongside the dashboard. It extends SVXReflector with server-to-server trunking, cluster TGs, and satellite support. Reflector admins can configure it entirely from the web UI at `/admin/reflector` — global settings, users, passwords, talkgroup rules, trunk peers, and satellite configuration. On save, the dashboard writes the configuration file and automatically restarts the container via the Docker socket.

Exposed ports:
- **5300** (TCP/UDP) — client connections
- **5302** (TCP) — trunk peer-to-peer links
- **5303** (TCP) — satellite connections

### redis

Used for four purposes:
1. **ActionCable adapter** — WebSocket pub/sub for live dashboard updates
2. **Audio pub/sub** — carries audio commands and Opus frames between Rails and the Go bridge
3. **Metadata cache** — stores web listener node info and the latest reflector snapshot
4. **GeuReflector state** — caches trunk link status, satellite status, cluster TGs, and reflector config/mode

## Data flow

### Dashboard updates

```
GeuReflector HTTP /status API
        ↓ (poll every 4s)
ReflectorListener (updater service)
        ├─→ Parse nodes, trunks, satellites, cluster_tgs from response
        ├─→ Diff nodes + trunk/satellite/cluster state against previous snapshot
        ├─→ Enrich web listener nodes with browser metadata from Redis
        ├─→ Enrich bridge nodes with D-STAR/DMR/YSF/M17 RX data from Redis
        ├─→ Persist node events + trunk/satellite events to node_events table (SQLite)
        ├─→ Sync cluster TGs to tgs table
        ├─→ Cache all state in Redis (snapshot, trunks, satellites, cluster_tgs)
        ├─→ Fetch /config endpoint periodically (mode detection, topology)
        └─→ Broadcast changed data via ActionCable → Redis → browsers
```

### Event types

The `node_events` table records 12 event types:

| Event | Trigger |
|---|---|
| `talking_start` | Node begins transmitting |
| `talking_stop` | Node stops transmitting |
| `tg_join` | Node selects a talkgroup |
| `tg_leave` | Node leaves a talkgroup |
| `connected` | Node appears in the snapshot |
| `disconnected` | Node disappears from the snapshot |
| `trunk_connected` | Trunk link connects to a peer |
| `trunk_disconnected` | Trunk link disconnects from a peer |
| `remote_talk_start` | Remote talker starts on a trunked TG |
| `remote_talk_stop` | Remote talker stops on a trunked TG |
| `satellite_connected` | Satellite connects to the reflector |
| `satellite_disconnected` | Satellite disconnects from the reflector |

Trunk and satellite events use the `source` column to identify the originating trunk peer.

### Audio path

```
RX (receiving audio from the reflector):

SVXReflector UDP → audio_bridge (Go) → Redis (audio:tg:<N>)
  → ActionCable → Browser → Opus decoder → AudioContext → speaker
                                            └→ AnalyserNode → S-meter + spectrum

TX (transmitting audio to the reflector):

Browser mic → MediaStreamTrackProcessor → Opus encoder
  → ActionCable (tx_audio) → Redis (audio:tx) → audio_bridge (Go)
  → SVXReflector UDP (protocol V2)
  └→ AnalyserNode → S-meter + spectrum
```

### Redis channels

| Channel | Direction | Purpose |
|---|---|---|
| `audio:commands` | Rails → Go | Connect, disconnect, select_tg commands |
| `audio:tx` | Rails → Go | PTT start/stop and audio frames (base64 Opus) |
| `audio:tg:<N>` | Go → Rails | Incoming audio frames for talkgroup N |
| `updates` | Updater → browsers | ActionCable dashboard update broadcasts |

### Redis keys

| Key | Type | Purpose |
|---|---|---|
| `reflector:snapshot` | String (JSON) | Latest reflector status snapshot (nodes only) |
| `reflector:trunks` | String (JSON) | GeuReflector trunk link status (connected, active_talkers, prefixes) |
| `reflector:satellites` | String (JSON) | GeuReflector satellite status (authenticated, active_tgs) |
| `reflector:cluster_tgs` | String (JSON) | GeuReflector cluster TG list (network-wide channels) |
| `reflector:config` | String (JSON) | GeuReflector `/config` endpoint cache (mode, topology) |
| `web_node_info` | Hash | Per-callsign browser metadata for web listeners |
| `dstar_rx:<callsign>` | String (JSON) | D-STAR RX metadata from XLX bridges (MYCALL, suffix, slow data text). Set with 30s TTL on voice start, updated with decoded text each superframe, deleted on stream end. |

## Web listener node enrichment

When a browser connects to the audio bridge, it sends metadata (browser name, version, geolocation, sysop name) via ActionCable. This metadata is stored in the `web_node_info` Redis hash. On each poll cycle, `ReflectorListener` merges this metadata into the reflector snapshot so web listener cards display the same info as real radio nodes.

For the map view, if a web listener has geolocation data in `nodeLocation` (format: `"lat, lon"`), the enrichment step builds the `qth` structure expected by Leaflet markers.
