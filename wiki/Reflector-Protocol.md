# Reflector Protocol

The audio bridge implements SVXReflector protocol **V2** (no encryption). This page documents the wire format as implemented in `audio_bridge/protocol.go`.

## TCP framing

All TCP messages use big-endian byte order:

```
[4 bytes: message length (uint32 BE)]
[2 bytes: message type (uint16 BE)]
[N bytes: payload]
```

Message length includes the type field (2 bytes) plus payload length.

## TCP message types

| Type | Name | Direction |
|---|---|---|
| 1 | Heartbeat | Both |
| 5 | ProtoVer | Client → Server |
| 10 | AuthChallenge | Server → Client |
| 11 | AuthResponse | Client → Server |
| 12 | AuthOk | Server → Client |
| 13 | Error | Server → Client |
| 100 | ServerInfo | Server → Client |
| 102 | NodeJoined | Server → Client |
| 103 | NodeLeft | Server → Client |
| 104 | TalkerStart | Both |
| 105 | TalkerStop | Both |
| 106 | SelectTG | Client → Server |
| 111 | NodeInfo | Client → Server |

## Connection handshake

```
Client                          Server
  │                               │
  ├── MsgProtoVer (2.0) ────────→│
  │                               │
  │←─── MsgAuthChallenge ────────┤
  │                               │
  ├── MsgAuthResponse ──────────→│  (HMAC-SHA1 of challenge with auth_key)
  │                               │
  │←─── MsgAuthOk ──────────────┤
  │                               │
  │←─── MsgServerInfo ──────────┤  (client_id, codecs)
  │                               │
  ├── MsgNodeInfo ──────────────→│  (JSON with callsign, sw, nodeClass, etc.)
  │                               │
  ├── UDP registration heartbeat→│  (type=1, client_id, seq=0)
  │                               │
```

## Message payloads

### MsgProtoVer (type 5)

```
[2B major (uint16 BE)][2B minor (uint16 BE)]
```

### MsgAuthChallenge (type 10)

```
[2B length (uint16 BE)][N bytes challenge nonce]
```

### MsgAuthResponse (type 11)

```
[2B callsign_len][callsign bytes][2B digest_len][20 bytes HMAC-SHA1 digest]
```

### MsgServerInfo (type 100)

```
[2B reserved][2B client_id][vector<string> nodes][vector<string> codecs]
```

### MsgNodeInfo V2 (type 111)

```
[2B json_len][JSON string]
```

JSON contains node metadata: `callsign`, `sw`, `swVer`, `nodeClass`, `nodeLocation`, `sysop`.

### MsgSelectTG (type 106)

```
[4B tg (uint32 BE)]
```

### MsgTalkerStart (type 104) / MsgTalkerStop (type 105)

```
[4B tg (uint32 BE)][2B callsign_len][callsign bytes]
```

## UDP framing (V2, no encryption)

```
[2B type (uint16 BE)][2B client_id (uint16 BE)][2B seq (uint16 BE)][payload]
```

### UDP message types

| Type | Name |
|---|---|
| 1 | Heartbeat |
| 101 | Audio |

### UDP Audio payload

```
[2B audio_length (uint16 BE)][N bytes Opus data]
```

## Serialization helpers

All string and vector fields use the same pattern:

- **String:** `[2B length (uint16 BE)][N bytes UTF-8]`
- **Byte vector:** `[2B length (uint16 BE)][N bytes]`
- **String vector:** `[2B count (uint16 BE)][count × string]`

## Heartbeats

- **TCP:** The server sends periodic heartbeats (type 1, empty payload). The client must respond with the same.
  - The bridge also sends its own TCP heartbeats every 10 seconds.
- **UDP:** The bridge sends UDP heartbeats every 15 seconds (`type=1, client_id, seq`).
