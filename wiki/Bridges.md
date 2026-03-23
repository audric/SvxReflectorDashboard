# Bridges

![Bridges](images/admin-bridges.png)

SVXLink bridges connect two audio endpoints — typically a local reflector and a remote reflector or EchoLink network. Each bridge runs as a separate Docker container (`svxlink-bridge-<id>`) with auto-generated configuration files.

Available bridge types are controlled by the `BRIDGE_TYPES` environment variable (comma-separated). Default: `reflector` only. Example: `BRIDGE_TYPES=reflector,xlx,zello`.

## Bridge types

### Reflector bridge

Connects two SVXReflector instances by running two `ReflectorV2` logics linked together. Each bridge has configurable talkgroup mappings that define which local TG maps to which remote TG. The generated config uses `HOSTS` and `HOST_PORT` directives (the modern SVXLink syntax).

Generated files:
- `svxlink.conf` — main SVXLink configuration with both logic sections and link definitions
- `node_info.json` — node metadata (class, location, sysop) sent to the reflector

### EchoLink bridge

Connects a local SVXReflector to the EchoLink network. Runs a `SimplexLogic` hosting the EchoLink module, linked to a `ReflectorV2` logic that connects to the local reflector.

Generated files:
- `svxlink.conf` — main configuration with SimplexLogic, ReflectorLogic, null audio devices, and link
- `ModuleEchoLink.conf` — EchoLink module settings (callsign, password, proxy, access control, description)
- `node_info.json` — node metadata

### XLX bridge

Connects a local SVXReflector to an XLX reflector via the **DCS** or **DExtra** protocol. Runs as a standalone Go binary (`xlx-bridge-<id>`) that transcodes audio between OPUS (SVXReflector) and AMBE (D-STAR).

Key features:
- **Protocol selection** — DCS (port 30051) or DExtra (port 30001), configurable in the admin UI
- **Reflector directory** — the admin form loads reflector lists from pistar.uk (DCS_Hosts.txt / DExtra_Hosts.txt), cached 24 hours with manual refresh
- **Audio transcoding** — OPUS ↔ AMBE via the D-STAR vocoder (MBEVocoder from DroidStar)
- **AGC** — automatic gain control on both audio paths
- **D-STAR metadata** — the originating SVXReflector callsign is set as MYCALL and slow data text (visible on D-STAR radios). The `-WEB` suffix is stripped for D-STAR compatibility.
- **D-STAR RX display** — when a D-STAR station transmits through the XLX reflector, the bridge publishes MYCALL and slow data text to Redis (`dstar_rx:<callsign>`), which the dashboard displays on the node card in real time
- **Echo suppression** — 1.5-second grace window after TX to ignore XLX echo frames with different stream IDs

Environment variables passed to the container:

| Variable | Purpose |
|---|---|
| `REFLECTOR_HOST` | Local SVXReflector hostname |
| `REFLECTOR_PORT` | Local SVXReflector port |
| `REFLECTOR_AUTH_KEY` | Authentication key for the local reflector |
| `REFLECTOR_TG` | Talkgroup to bridge |
| `XLX_HOST` | XLX reflector hostname or IP |
| `XLX_PORT` | Protocol port (30051 for DCS, 30001 for DExtra) |
| `XLX_MODULE` | Target module (A-Z) |
| `XLX_PROTOCOL` | `DCS` or `DEXTRA` (default: `DCS`) |
| `XLX_REFLECTOR_NAME` | Reflector name (e.g. DCS585, XRF585) |
| `CALLSIGN` | Bridge callsign on the local reflector |
| `XLX_CALLSIGN` | Link callsign for the XLX connection (8 chars) |
| `XLX_MYCALL` | MYCALL field in D-STAR voice headers |
| `XLX_MYCALL_SUFFIX` | MYCALL suffix (default "AMBE") |
| `REDIS_URL` | Redis connection for D-STAR RX metadata publishing |

#### DCS vs DExtra

| | DCS | DExtra |
|---|---|---|
| Default port | 30051 | 30001 |
| Packet format | 100-byte combined header+voice | 56-byte DSVT header + 27-byte voice frames |
| Host list | DCS_Hosts.txt (DCSxxx entries) | DExtra_Hosts.txt (XRFxxx entries) |
| RPT2 field | Reflector DCS name (e.g. DCS585) | Derived XRF name (e.g. XRF585) |

### DMR bridge

Connects a local SVXReflector to a DMR network (e.g. BrandMeister). Runs as a standalone Go binary (`dmr-bridge-<id>`).

### YSF bridge

Connects a local SVXReflector to a YSF (Yaesu System Fusion) network. Runs as a standalone Go binary (`ysf-bridge-<id>`).

### AllStar bridge

Connects a local SVXReflector to the AllStar network via IAX2. Runs as a standalone Go binary (`allstar-bridge-<id>`).

### Zello bridge

Connects a local SVXReflector to a Zello consumer channel. Runs as a standalone Go binary (`zello-bridge-<id>`) that transcodes audio between OPUS 48kHz (SVXReflector) and OPUS 16kHz/60ms (Zello).

Key features:
- **WebSocket connection** to Zello consumer API (`wss://zello.io/ws`)
- **JWT RS256 authentication** with Zello developer credentials (issuer ID + private key)
- **OPUS transcoding** — no vocoder needed, just sample rate conversion (48kHz ↔ 16kHz). OPUS handles resampling internally.
- **Half-duplex** — Zello is PTT-based, collision handling prevents both sides from talking simultaneously
- **Reconnection** — automatic reconnect with exponential backoff for both SVX and Zello connections

**Note:** The Zello Channel API does not support password-protected channels. If your channel has a password, you must remove it for the bridge to work.

Environment variables passed to the container:

| Variable | Purpose |
|---|---|
| `REFLECTOR_HOST` | Local SVXReflector hostname |
| `REFLECTOR_PORT` | Local SVXReflector port |
| `REFLECTOR_AUTH_KEY` | Authentication key for the local reflector |
| `REFLECTOR_TG` | Talkgroup to bridge |
| `CALLSIGN` | Bridge callsign on the local reflector |
| `ZELLO_USERNAME` | Zello account username |
| `ZELLO_PASSWORD` | Zello account password |
| `ZELLO_CHANNEL` | Zello channel name |
| `ZELLO_CHANNEL_PASSWORD` | Channel password (if applicable) |
| `ZELLO_ISSUER_ID` | Developer issuer ID from [developers.zello.com](https://developers.zello.com/keys) |
| `ZELLO_PRIVATE_KEY_FILE` | Path to RS256 private key PEM file (mounted at `/etc/zello/private_key.pem`) |
| `REDIS_URL` | Redis connection (optional, for RX metadata) |

#### Zello developer credentials

1. Register at [developers.zello.com](https://developers.zello.com/)
2. Complete your developer profile
3. Click "Keys" to get your **Issuer ID** and **Private Key**
4. Paste both into the bridge configuration form

Audio path:
```
SVX → Zello: OPUS 48kHz → decode to PCM 16kHz → buffer 60ms → encode OPUS 16kHz → WebSocket
Zello → SVX: OPUS 16kHz/60ms → decode to PCM 48kHz → split 3×20ms → encode OPUS 48kHz → UDP
```

## Config auto-generation

Every time a bridge is saved (`after_save`), the app:

1. Creates the config directory at `bridge/<id>/`
2. Takes a snapshot backup of existing config files (see [Backups](#backups))
3. Generates config files appropriate for the bridge type
4. Writes `node_info.json` with node class, location, and sysop info (SVXLink bridges)
5. For reflector bridges with a custom CA bundle or local reflector CA, writes `ca-bundle.crt`
6. For Zello bridges, writes the private key PEM file

The local reflector's CA bundle is fetched automatically from the running `svxreflector` container via the Docker socket.

## node_info.json

Each SVXLink bridge writes a `node_info.json` file that the SVXLink process sends to the reflector on connect. Contents:

| Field | Value |
|---|---|
| `nodeClass` | `"echolink"`, `"bridge"`, `"xlx"`, `"zello"`, etc. |
| `nodeLocation` | Custom location string, defaults to bridge name |
| `hidden` | Always `false` |
| `sysop` | Sysop name (optional) |
| `links` | Array of `{localTg, remoteTg}` objects |
| `remoteHost` | Remote host (reflector bridges, XLX, Zello) |

The `links` and `remoteHost` fields are displayed on the dashboard node cards and map popups, showing which talkgroups the bridge connects.

## Talkgroup mappings (reflector bridges)

Reflector bridges support multiple TG mappings. Each mapping defines:

| Field | Purpose |
|---|---|
| `local_tg` | Talkgroup number on the local reflector |
| `remote_tg` | Talkgroup number on the remote reflector |
| `timeout` | Link timeout in seconds (0 = no timeout) |

Each mapping generates a `[LinkN]` section in `svxlink.conf` with `CONNECT_LOGICS` wiring the two logics together.

## Backups

Config files are backed up as **snapshots** before each save. Snapshots are stored in `bridge/<id>/backups/<YYYYMMDD_HHMMSS>/` and contain copies of all existing config files at that point in time.

- Maximum **10 snapshots** per bridge (oldest pruned automatically)
- Legacy `.bak` files are auto-migrated into the snapshot format on first save
- Snapshots can be viewed from the bridge list via the clock icon button

## Archive on delete

When a bridge is destroyed, its entire config directory is moved to `bridge/_archive/<id>_<name>_<timestamp>/` instead of being deleted. Archives are retained for **30 days** and then purged automatically.

Purge runs:
- On application boot (via initializer)
- On every bridge save
- On every bridge delete

## PKI and certificates

Reflector bridges can include certificate subject fields (country, org, OU, locality, state, given name, surname, email) that SVXLink uses for TLS client certificate generation.

If the local reflector has a CA bundle (`ca-bundle.crt`), the bridge fetches it automatically and writes a combined CA bundle file. Bridges can also specify a custom `remote_ca_bundle` for the remote reflector's CA.

## Running without Caddy

If you run a reverse proxy on the host (e.g. nginx-proxy-manager), disable Caddy with a `docker-compose.override.yml`:

```yaml
services:
  caddy:
    profiles: ["disabled"]
```

Then add the `web` container to your reverse proxy's Docker network (or point the proxy to the container's name/IP on port 3000). No host port mapping is needed — the reverse proxy communicates directly with the container on the Docker network.

## File layout

```
bridge/
  <id>/
    svxlink.conf            # Main SVXLink config (reflector/echolink bridges)
    ModuleEchoLink.conf     # EchoLink module config (EchoLink bridges only)
    node_info.json          # Node metadata (SVXLink bridges)
    ca-bundle.crt           # Combined CA bundle (if applicable)
    xlx_bridge.env          # XLX bridge env config
    zello_bridge.env        # Zello bridge env config
    zello_private_key.pem   # Zello JWT private key
    backups/
      20260307_143022/      # Snapshot directories
        svxlink.conf
        node_info.json
      20260307_150510/
        ...
  _archive/                 # Deleted bridge configs (30-day retention)
    3_my-bridge_20260301_120000/
      svxlink.conf
      ...
```
