# Audio Bridge

The audio bridge is a standalone Go binary (`audio_bridge/`) that connects browsers to the SVXReflector using protocol V2. It manages a single on-demand session and relays audio in both directions via Redis.

## How it works

1. The bridge starts and subscribes to two Redis channels: `audio:commands` and `audio:tx`
2. When a user tunes in from the browser, Rails publishes a `connect` command to `audio:commands`
3. The bridge establishes a TCP + UDP connection to the reflector, authenticates, and selects the requested talkgroup
4. Incoming audio (Opus frames via UDP) is base64-encoded and published to `audio:tg:<N>` in Redis
5. Rails picks up the frames via ActionCable and forwards them to the browser
6. For TX: the browser sends Opus frames via ActionCable → Rails → Redis (`audio:tx`) → bridge → reflector UDP

## Redis commands

Commands are JSON messages published to `audio:commands`:

### connect

```json
{
  "action": "connect",
  "tg": 1,
  "callsign": "F4ABC-WEB",
  "auth_key": "secret",
  "sw": "Chrome",
  "sw_ver": "122.0",
  "node_class": "bridge",
  "node_location": "48.86, 2.35",
  "sysop": "Jean Dupont"
}
```

Starts a new session: connects to the reflector, authenticates, selects the TG, and begins streaming audio.

### disconnect

```json
{ "action": "disconnect" }
```

Closes the current session.

### select_tg

```json
{ "action": "select_tg", "tg": 2, "callsign": "F4ABC-WEB" }
```

Changes the talkgroup on an active session. If no session exists, starts one. If the callsign changed, reconnects with the new identity.

Commands published to `audio:tx`:

### ptt_start / ptt_stop

```json
{ "action": "ptt_start", "tg": 1, "callsign": "F4ABC-WEB" }
{ "action": "ptt_stop", "tg": 1, "callsign": "F4ABC-WEB" }
```

Sends TalkerStart/TalkerStop protocol messages to the reflector.

### audio

```json
{ "action": "audio", "audio": "<base64 Opus frame>" }
```

Sends a single Opus frame to the reflector via UDP.

## Node metadata

When connecting, the bridge sends a `MsgNodeInfo` JSON to the reflector containing:

| Field | Source | Example |
|---|---|---|
| `callsign` | User's callsign + `-WEB` suffix | `F4ABC-WEB` |
| `sw` | Browser name (parsed from User-Agent) | `Chrome` |
| `swVer` | Browser version | `122.0.6261` |
| `nodeClass` | Hardcoded | `bridge` |
| `nodeLocation` | Browser geolocation (if permitted) | `48.86, 2.35` |
| `sysop` | User's name | `Jean Dupont` |

This metadata flows through the reflector's `/status` endpoint and appears on the dashboard node card.

## Session lifecycle

```
IDLE → connect command → TCP handshake → auth → select TG → STREAMING
STREAMING → disconnect command → close TCP/UDP → IDLE
STREAMING → TCP read error (reflector dropped) → cleanup → IDLE
STREAMING → select_tg command → send SelectTG msg → STREAMING (new TG)
```

## Building

```bash
cd audio_bridge
go build -o audio_bridge .
```

Or via Docker:

```bash
docker compose build audio_bridge
```

## Source files

| File | Purpose |
|---|---|
| `main.go` | Entry point, Redis subscriber, session manager |
| `client.go` | TCP/UDP reflector client with handshake, heartbeats, audio I/O |
| `protocol.go` | Wire format builders and parsers for SVXReflector protocol V2 |
| `Dockerfile` | Multi-stage Go build for production |
