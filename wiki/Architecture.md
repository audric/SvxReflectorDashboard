# Architecture

## Services

```
svxreflector  → GeuReflector instance (SVXReflector-compatible), configurable via the web admin UI
caddy         → Reverse proxy with automatic HTTPS (Let's Encrypt)
web           → Rails app (Puma), serves the dashboard on port 3000
updater       → Background process running ReflectorListener
audio_bridge  → Go binary, bridges browser audio ↔ reflector (SVXReflector protocol V2)
xlx_bridge    → Go binary, D-STAR XLX bridge (DCS + DExtra protocols, OPUS ↔ AMBE transcoding)
dmr_bridge    → Go binary, DMR bridge (OPUS ↔ AMBE transcoding via MMDVM Homebrew)
ysf_bridge    → Go binary, YSF bridge (OPUS ↔ IMBE transcoding)
allstar_bridge→ Go binary, AllStar bridge (OPUS ↔ µLaw via IAX2)
zello_bridge  → Go binary, Zello channel bridge (OPUS 48kHz ↔ 16kHz via WebSocket)
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
        ↓ (poll every 1s)
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

### Audio path — analog radio (core)

```
Analog Radio ↔ RF ↔ SVXLink Repeater ↔ OPUS/UDP ↔ SVX Reflector ↔ OPUS/UDP ↔ Other SVXLink Nodes
```

This is the fundamental path — all other paths branch from the reflector. No HPF/LPF/AGC is applied here; audio filtering on the repeater side is handled by SVXLink's own audio chain (PREAMP, PEAK_METER, LADSPA plugins in `svxlink.conf`), configured by the repeater operator.

### Audio path — web listener

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

### Audio path — protocol bridges

All Go-based bridges (XLX, DMR, YSF, AllStar, Zello) apply a three-stage audio processing pipeline:

```
SVX → Remote:  OPUS decode → PCM → HPF 300Hz → LPF 3kHz → AGC + Limiter → Vocoder encode
Remote → SVX:  Vocoder decode → PCM → HPF 300Hz → LPF 3kHz → AGC + Limiter → OPUS encode
```

- **HPF/LPF** — 2nd-order Butterworth bandpass (300–3000 Hz), removes hum and HF noise
- **AGC** — automatic gain control with configurable attack/decay rates
- **Hard limiter** — absolute ceiling at 90% of full scale, prevents vocoder clipping

All parameters are configurable per-bridge from the admin UI. See [[Bridges#audio-processing]] for details.

### Audio path — cross-protocol (e.g. Analog ↔ D-STAR)

When an analog FM user talks to a D-STAR user, the audio traverses two hops through the reflector:

```
Analog → D-STAR:
  Analog Radio → RF → SVXLink Repeater → OPUS/UDP → SVX Reflector → OPUS/UDP →
  XLX Bridge: OPUS decode → PCM 8kHz → HPF → LPF → AGC → AMBE encode →
  XLX Reflector → DCS/DExtra → D-STAR Repeater → RF → D-STAR Radio

D-STAR → Analog:
  D-STAR Radio → RF → D-STAR Repeater → AMBE/DCS|DExtra → XLX Reflector →
  XLX Bridge: AMBE decode → PCM 8kHz → HPF → LPF → AGC → OPUS encode →
  SVX Reflector → OPUS/UDP → SVXLink Repeater → RF → Analog Radio
```

Note: HPF/LPF/AGC only applies inside the Go bridge. The SVXLink repeater's audio chain is separate and configured by the repeater operator.

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

## GeuReflector extensions

The dashboard is built on [GeuReflector](https://github.com/audric/geureflector), a drop-in replacement for SVXReflector that adds server-to-server networking. It is 100% backward-compatible — the dashboard works with vanilla SVXReflector too (trunk/satellite features simply remain empty).

### Trunking

Trunk links are persistent TCP connections between independent reflector instances that share talkgroups as parallel voice channels. SvxLink client nodes connect normally and are unaware of the trunk.

```
                     ┌────────────────┐
                     │  TRUNK_2       │
  Reflector A        │  Host: B       │        Reflector B
  LOCAL_PREFIX=1     │  Port: 5302    │        LOCAL_PREFIX=2
  ┌──────────┐       │  Secret: ***   │       ┌──────────┐
  │ TG 1xx   │◄─────►│  REMOTE_PFX=2 │◄─────►│ TG 2xx   │
  │ nodes    │       └────────────────┘       │ nodes    │
  └──────────┘                                └──────────┘
       │              CLUSTER_TGS=222               │
       └──────────── TG 222 broadcasts ────────────►┘
                     to ALL peers
```

TG ownership is prefix-based: `LOCAL_PREFIX=1` means this reflector owns TGs starting with `1` (1, 10, 100, 1234…). Each peer gets a `[TRUNK_x]` config section with `HOST`, `PORT`, `SECRET`, `REMOTE_PREFIX`.

**Cluster TGs** (`CLUSTER_TGS=222,2221,91`) are broadcast to all trunk peers regardless of prefix ownership — network-wide channels like BrandMeister nationwide groups.

### Satellites

Satellites are lightweight relay instances that connect to a parent reflector instead of joining the full mesh. The parent always wins talker arbitration.

```
  Parent reflector                     Satellite
  ┌──────────┐         TCP 5303        ┌──────────┐
  │ [SATELLITE]│◄──────────────────────│ SATELLITE_OF=parent │
  │ PORT=5303  │     authenticated      │ SATELLITE_ID=sat-1 │
  │ SECRET=*** │                        │ SATELLITE_SECRET=** │
  └──────────┘                         └──────────┘
```

Config: `SATELLITE_OF`, `SATELLITE_PORT`, `SATELLITE_SECRET`, `SATELLITE_ID` in `[GLOBAL]` on the satellite side. `[SATELLITE]` section with `LISTEN_PORT` and `SECRET` on the parent side.

### Mode detection

The poller fetches the `/config` endpoint every 60 seconds and caches the result in Redis at `reflector:config`. The `mode` field (`"reflector"` or `"satellite"`) drives the mode-aware UI — satellite mode hides trunk management and shows the parent connection status instead.

### Status API extensions

GeuReflector's `/status` response adds three top-level keys alongside `nodes`:

```json
{
  "nodes": { ... },
  "trunks": {
    "TRUNK_2": {
      "host": "reflector-b.example.com",
      "port": 5302,
      "connected": true,
      "local_prefix": ["1"],
      "remote_prefix": ["2"],
      "active_talkers": { "222": "IW1GEU" }
    }
  },
  "cluster_tgs": [222, 2221, 91],
  "satellites": {
    "my-sat": {
      "id": "my-sat",
      "authenticated": true,
      "active_tgs": [1, 100]
    }
  }
}
```

The `/config` endpoint returns static topology:

```json
{
  "mode": "reflector",
  "local_prefix": ["1"],
  "cluster_tgs": [222, 2221, 91],
  "listen_port": "5300",
  "http_port": "8080",
  "trunks": {
    "TRUNK_2": { "host": "...", "port": 5302, "remote_prefix": ["2"] }
  },
  "satellite_server": { "listen_port": "5303", "connected_count": 2 }
}
```

### Dashboard integration

The poller caches all extended data in Redis and broadcasts changes via ActionCable. The dashboard renders:

- **Trunk status panel** — connected/disconnected indicator per peer, active remote talkers with TG
- **Satellite panel** — authenticated status, active TGs per satellite
- **Cluster TG badges** — visual indicators distinguishing cluster-wide TGs from local/remote
- **Map trunk info** — connection status overlay control
- **Stats** — per-trunk traffic rankings, cluster TG usage analytics
- **Navbar SAT badge** — visible when running in satellite mode

All panels are conditionally rendered — they stay hidden when running vanilla SVXReflector or when no trunks/satellites are configured.

## Web listener node enrichment

When a browser connects to the audio bridge, it sends metadata (browser name, version, geolocation, sysop name) via ActionCable. This metadata is stored in the `web_node_info` Redis hash. On each poll cycle, `ReflectorListener` merges this metadata into the reflector snapshot so web listener cards display the same info as real radio nodes.

For the map view, if a web listener has geolocation data in `nodeLocation` (format: `"lat, lon"`), the enrichment step builds the `qth` structure expected by Leaflet markers.
