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
- **Voice bandpass filter** — 2nd-order Butterworth HPF (300 Hz) + LPF (3000 Hz) removes hum, DC offset, and high-frequency noise before the vocoder
- **AGC** — automatic gain control with hard limiter on both audio paths
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
| `FILTER_SVX_TO_EXT_HPF_CUTOFF` | High-pass filter cutoff in Hz, SVX→XLX direction (default: 300, 0 = disabled) |
| `FILTER_SVX_TO_EXT_LPF_CUTOFF` | Low-pass filter cutoff in Hz, SVX→XLX direction (default: 3000, 0 = disabled) |
| `FILTER_EXT_TO_SVX_HPF_CUTOFF` | High-pass filter cutoff in Hz, XLX→SVX direction (default: 300) |
| `FILTER_EXT_TO_SVX_LPF_CUTOFF` | Low-pass filter cutoff in Hz, XLX→SVX direction (default: 3000) |
| `AGC_SVX_TO_EXT_TARGET_LEVEL` | AGC target peak level 0.0–1.0 (default: 0.3) |
| `AGC_SVX_TO_EXT_DECAY_RATE` | AGC decay rate 0.0–1.0 (default: 0.3) |
| `AGC_SVX_TO_EXT_MIN_GAIN` | AGC minimum gain / attenuation floor (default: 0.1) |
| `AGC_SVX_TO_EXT_MAX_GAIN` | AGC maximum amplification (default: 4.0) |
| `AGC_SVX_TO_EXT_LIMIT_LEVEL` | Hard limiter threshold 0.0–1.0 (default: 0.9, 0 = disabled) |

All `AGC_*` and `FILTER_*` variables exist for both directions (`SVX_TO_EXT_*` and `EXT_TO_SVX_*`). See [Audio Processing](#audio-processing) for details.

#### DCS vs DExtra

| | DCS | DExtra |
|---|---|---|
| Default port | 30051 | 30001 |
| Packet format | 100-byte combined header+voice | 56-byte DSVT header + 27-byte voice frames |
| Host list | DCS_Hosts.txt (DCSxxx entries) | DExtra_Hosts.txt (XRFxxx entries) |
| RPT2 field | Reflector DCS name (e.g. DCS585) | Derived XRF name (e.g. XRF585) |

### DMR bridge

Connects a local SVXReflector to a DMR network (e.g. BrandMeister). Runs as a standalone Go binary (`dmr-bridge-<id>`) that transcodes audio between OPUS (SVXReflector) and AMBE (DMR). Includes voice bandpass filter and AGC on both directions.

### YSF bridge

Connects a local SVXReflector to a YSF (Yaesu System Fusion) network. Runs as a standalone Go binary (`ysf-bridge-<id>`) that transcodes audio between OPUS (SVXReflector) and IMBE (YSF). Includes voice bandpass filter and AGC on both directions.

### AllStar bridge

Connects a local SVXReflector to the AllStar network via IAX2. Runs as a standalone Go binary (`allstar-bridge-<id>`) that transcodes audio between OPUS (SVXReflector) and µLaw PCM (AllStar). Includes voice bandpass filter and AGC on both directions.

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
SVX → Zello: OPUS 48kHz → PCM 48kHz → HPF → LPF → AGC → PCM 16kHz → buffer 60ms → OPUS 16kHz → WebSocket
Zello → SVX: OPUS 16kHz/60ms → PCM 48kHz → HPF → LPF → AGC → split 3×20ms → OPUS 48kHz → UDP
```

### Mumble bridge

Connects a local SVXReflector talkgroup to a channel on a **self-hosted Mumble (Murmur) voice server**. Unlike the other bridge types — which connect to an external network — the Mumble bridge pairs with the bundled `mumble` Docker service that your users connect to directly with any Mumble client (desktop, mobile, [Mumla](https://mumla-app.gitlab.io/), etc.). It turns a talkgroup into a VoIP room: audio keyed on the TG is heard in the Mumble channel, and audio spoken in the channel is relayed onto the TG.

Two pieces work together:

- **`mumble` server** — the official `mumblevoip/mumble-server` image **extended with a ZeroC Ice management client** (`mumble_ice.py`), listening on TCP+UDP port `64738`. Users connect their own Mumble clients here. The dashboard manages registrations and ACLs on the *running* server via Ice (which stays bound to `127.0.0.1` inside the container — never network-exposed). Its SQLite DB lives on the `mumble_data` volume, read by the `web` container for the System Info tab.
- **`mumble-bridge-<id>` bot** — a standalone Go binary (gumble client + libopus) that logs into the Mumble server as a registered bot, joins the bridge's channel, and relays audio to/from the SVXReflector TG.

Key features:
- **Self-hosted, no external account** — you run the voice server; there are no third-party credentials or rate limits
- **Per-user access from the dashboard** — Mumble logins and permissions are driven by the dashboard's user list, not configured manually on the server (see [User access model](#user-access-model) below)
- **OPUS end to end** — both sides are 48 kHz Opus, so no vocoder/transcoding is needed (just frame re-packing: SVX 20 ms ↔ Mumble 10 ms)
- **Half-duplex, first-talker-wins** — while the TG is talking, Mumble input is held off, and vice versa; a second concurrent Mumble talker is ignored until the active one stops
- **VOX talk detection** — talk-spurt boundaries on the Mumble side are derived from a 300 ms silence gap (gumble delivers one continuous stream per user)
- **Voice bandpass filter + AGC** on both directions (see [Audio processing](#audio-processing))
- **Auto-reconnect** with exponential backoff on either side dropping

#### User access model

The Mumble server is **locked down by default** — guests cannot enter, and only authorised users can speak. The dashboard is the single source of truth; whenever a relevant user or bridge changes, `MumbleSync` (`app/services/mumble_sync.rb`) execs `mumble_ice.py` inside the mumble container, which applies the changes to the **running** server through its Ice interface — **no restart**, so bridges and connected clients are never dropped (this scales to any number of users):

- **Registered-only Enter, transmit-only Speak** — the base lockdown ACL is (re)applied idempotently on every sync via Ice, so even a fresh deployment is locked down the first time a user or bridge is synced. The `tx` group grants Speak; a user is added to it when they have **Can transmit** (or are an admin). Everyone else can listen only.
- **Human users** — each user with the **Allow Mumble** flag and a callsign gets a Mumble login (username = callsign, password = an auto-minted token). See [[User-Management#mumble-access]] for granting access and the self-service connection page.
- **Bot accounts** — each Mumble bridge logs in as its own registered account (username = the bridge **CALLSIGN**, password auto-generated), placed in the `tx` group so it can inject TG audio.
- **Permanent channels** — each bridge's target channel is pre-created as a permanent child of Root with inherited ACL, so listeners stay put across bridge restarts instead of being bounced to Root.

Environment variables passed to the bridge container:

| Variable | Purpose |
|---|---|
| `REFLECTOR_HOST` | Local SVXReflector hostname |
| `REFLECTOR_PORT` | Local SVXReflector port |
| `REFLECTOR_AUTH_KEY` | Authentication key for the local reflector |
| `REFLECTOR_TG` | Talkgroup to bridge |
| `CALLSIGN` | Bridge callsign on the local reflector (also the bot's Mumble username) |
| `MUMBLE_HOST` | Mumble server hostname (`mumble` inside Docker) |
| `MUMBLE_PORT` | Mumble server port (default 64738) |
| `MUMBLE_USERNAME` | Bot login (set to the bridge callsign) |
| `MUMBLE_PASSWORD` | Bot password (auto-generated, stored as `mumble_bot_password`) |
| `MUMBLE_CHANNEL` | Channel name the bot joins / relays |
| `REDIS_URL` | Redis connection (optional) |

All `FILTER_*` and `AGC_*` variables (both directions) apply as for the other Go bridges.

> **Server admin vs. dashboard:** the bundled server's SuperUser password comes from `MUMBLE_SUPERUSER_PASSWORD`. You normally never need it — accounts and ACLs are managed from the dashboard — but it is available for direct administration if required. The public host/port shown to users on `/account` come from `MUMBLE_PUBLIC_HOST` / `MUMBLE_PUBLIC_PORT` (defaulting to `DOMAIN` / `64738`).

#### Setting up a Mumble bridge

1. Enable the type: add `mumble` to `BRIDGE_TYPES` in `.env` (e.g. `BRIDGE_TYPES=reflector,mumble`) and set `MUMBLE_SUPERUSER_PASSWORD`.
2. Create the bridge at `/admin/bridges`: set a **CALLSIGN** (the bot's reflector + Mumble login, e.g. `F4ABC-MUM`), the local reflector **HOST/PORT/AUTH_KEY/TALKGROUP**, and the Mumble **HOST** (`mumble`), **PORT** (`64738`), and **CHANNEL** name. The bot password is generated automatically.
3. Grant users access: edit each user at `/admin/users` and check **Allow Mumble** (and **Can transmit** if they should be able to talk, not just listen).
4. Users connect their Mumble client using the server/port/username/token shown on their `/account` page.

## Audio processing

All Go-based bridges (XLX, DMR, YSF, AllStar, Zello, IAX, SIP, Mumble) apply a three-stage audio processing pipeline on both directions (reflector→remote and remote→reflector).

> **Note:** This processing only applies inside the Go bridge binaries. The core analog path (radio → SVXLink repeater → reflector) has no filtering from the dashboard — audio processing on the repeater side is handled by SVXLink's own audio chain (PREAMP, PEAK_METER, LADSPA plugins), configured by the repeater operator.

```
PCM decode → HPF (300 Hz) → LPF (3000 Hz) → AGC + Hard Limiter → Vocoder encode
```

### Voice bandpass filter

A 2nd-order Butterworth biquad filter pair restricts audio to the standard voice band:

| Parameter | Default | Purpose |
|---|---|---|
| HPF cutoff | 300 Hz | Removes 50/60 Hz mains hum, DC offset, and low-frequency rumble |
| LPF cutoff | 3000 Hz | Removes high-frequency noise above the voice band |

Set either cutoff to `0` to disable that filter stage. The filter runs before the AGC so that gain adjustments operate on voice-only content (not on hum or noise).

### AGC (Automatic Gain Control)

Normalizes audio levels to a target peak, with configurable attack/decay rates:

| Parameter | Default | Purpose |
|---|---|---|
| Target level | 0.3 (30%) | Target peak level as a fraction of full scale |
| Attack rate | 0.01 | How fast gain increases for quiet signals (slow = smooth) |
| Decay rate | 0.3 | How fast gain decreases for loud signals (fast = prevents clipping) |
| Max gain | 4.0 (12 dB) | Maximum amplification for quiet signals |
| Min gain | 0.1 (−20 dB) | Minimum gain — how much a loud signal can be attenuated |
| Hard limiter | 0.9 (90%) | Absolute ceiling — samples above this level are clamped |

The hard limiter runs after AGC as a safety net: even if the AGC can't react fast enough to a sudden loud signal, the limiter prevents clipping at the vocoder input.

### Configuration

All parameters are configurable per-bridge from the admin UI (collapsible "Voice Filter" and "Audio AGC" sections in the bridge edit form). The same values apply to both directions. Changes take effect on bridge restart.

Environment variable prefixes: `FILTER_SVX_TO_EXT_*`, `FILTER_EXT_TO_SVX_*`, `AGC_SVX_TO_EXT_*`, `AGC_EXT_TO_SVX_*`.

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
| `nodeClass` | `"echolink"`, `"bridge"`, `"xlx"`, `"zello"`, `"mumble"`, etc. |
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
    mumble_bridge.env       # Mumble bridge env config
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
