# Architecture

## Services

```
web           → Rails app (Puma), serves the dashboard on port 3000
updater       → Background process running ReflectorListener
audio_bridge  → Go binary, bridges browser audio ↔ reflector (SVXReflector protocol V2)
redis         → ActionCable adapter + audio pub/sub + web node metadata cache
db_data       → Named Docker volume persisting the SQLite database (storage/)
```

### web

The main Rails application served by Puma. Handles HTTP requests, renders HAML templates, and manages ActionCable WebSocket connections. Runs in Docker with the `RAILS_ENV=production` environment.

### updater

A background Rails runner process that starts `ReflectorListener`. It polls the reflector's HTTP status API, diffs node state, persists events to SQLite, and broadcasts changes over ActionCable via Redis.

### audio_bridge

A standalone Go binary that speaks the SVXReflector protocol V2 (TCP + UDP). It listens for commands on Redis channels and bridges audio between browsers and the reflector. See [[Audio Bridge]] for details.

### redis

Used for three purposes:
1. **ActionCable adapter** — WebSocket pub/sub for live dashboard updates
2. **Audio pub/sub** — carries audio commands and Opus frames between Rails and the Go bridge
3. **Metadata cache** — stores web listener node info and the latest reflector snapshot

## Data flow

### Dashboard updates

```
SVXReflector HTTP /status API
        ↓ (poll every 4s)
ReflectorListener (updater service)
        ├─→ Diff against previous snapshot
        ├─→ Enrich web listener nodes with browser metadata from Redis
        ├─→ Persist events to node_events table (SQLite)
        ├─→ Cache snapshot in Redis (reflector:snapshot)
        └─→ Broadcast changed nodes via ActionCable → Redis → browsers
```

### Event types

The `node_events` table records 6 event types:

| Event | Trigger |
|---|---|
| `talking_start` | Node begins transmitting |
| `talking_stop` | Node stops transmitting |
| `tg_join` | Node selects a talkgroup |
| `tg_leave` | Node leaves a talkgroup |
| `connected` | Node appears in the snapshot |
| `disconnected` | Node disappears from the snapshot |

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
| `reflector:snapshot` | String (JSON) | Latest reflector status snapshot |
| `web_node_info` | Hash | Per-callsign browser metadata for web listeners |

## Web listener node enrichment

When a browser connects to the audio bridge, it sends metadata (browser name, version, geolocation, sysop name) via ActionCable. This metadata is stored in the `web_node_info` Redis hash. On each poll cycle, `ReflectorListener` merges this metadata into the reflector snapshot so web listener cards display the same info as real radio nodes.

For the map view, if a web listener has geolocation data in `nodeLocation` (format: `"lat, lon"`), the enrichment step builds the `qth` structure expected by Leaflet markers.
