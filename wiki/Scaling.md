# Scaling

This page covers resource usage and practical limits when running multiple bridges.

## Resource usage per bridge type

### XLX bridge (D-STAR)

| Resource | Idle | Active (one audio stream) |
|---|---|---|
| RAM | ~15 MB | ~15 MB |
| CPU | Near zero (heartbeats only) | 2-5% of one core (AMBE vocoder) |
| Network | ~1 kbps (keepalives) | ~3-8 kbps per direction |
| Sockets | 2 UDP + 1 TCP | Same |
| Disk | ~88 MB (Docker image, shared) | Same |

The software AMBE vocoder (MBEVocoder from DroidStar) is the most CPU-intensive component. Each concurrent voice stream requires real-time PCM-to-AMBE transcoding in both directions.

### Reflector bridge (SVXLink)

| Resource | Idle | Active |
|---|---|---|
| RAM | ~50-80 MB | ~50-80 MB |
| CPU | Near zero | <1% (no transcoding, audio passthrough) |
| Network | ~1 kbps | ~8-16 kbps per direction (OPUS) |
| Sockets | 2 UDP + 2 TCP | Same |
| Disk | ~200 MB (Docker image, shared) | Same |

Reflector bridges are heavier on memory but lighter on CPU because they relay OPUS audio natively without transcoding.

### EchoLink bridge

Similar to reflector bridges in resource usage. The EchoLink module adds minimal overhead.

### DMR bridge

Same resource profile as the XLX bridge — uses AMBE+2 vocoder (MBEVocoder). ~15 MB RAM idle, 2-5% CPU per active stream.

### YSF bridge (Yaesu System Fusion)

Same as DMR bridge — AMBE+2 vocoder, same CPU/RAM profile. 5 AMBE frames per packet (vs 3 for DMR).

### AllStar bridge

Lighter than vocoder bridges — no vocoder needed (ulaw↔PCM is a lookup table). ~10 MB RAM, <1% CPU per active stream.

## What scales well

- **Network bandwidth** — audio streams are low bandwidth (3-16 kbps each). Even 50 concurrent bridges would use under 1 Mbps.
- **Memory** — 10 XLX bridges use ~150 MB; 10 SVXLink bridges use ~500-800 MB. Modest by modern standards.
- **Idle bridges** — a bridge with no active audio uses essentially zero CPU. Only heartbeat packets are exchanged.
- **Disk** — Docker image layers are shared between containers of the same type. Config files are negligible.

## Bottlenecks

### CPU (XLX bridges with active audio)

The MBEVocoder software vocoder is the primary scaling constraint. Each concurrent voice stream on an XLX bridge consumes 2-5% of a modern CPU core for AMBE encode + decode.

OPUS encode/decode (used on the SVXReflector side) is much lighter at ~0.5% per stream.

### SVXReflector connections

Each bridge is a connected node on the reflector. With many bridges on the same talkgroup, every audio packet is relayed to all of them. The reflector's connection tracking and audio distribution scales linearly with connected nodes.

### Docker overhead

Each bridge is a separate container. Docker itself handles hundreds of containers without issue, but container creation/destruction during start/stop operations takes 1-2 seconds each.

## Practical limits

| Server | Max bridges (mixed) | Max XLX with concurrent audio |
|---|---|---|
| Raspberry Pi 4 (4 cores, 4 GB) | ~10-15 | ~3-5 |
| Raspberry Pi 5 (4 cores, 8 GB) | ~20-25 | ~8-12 |
| Typical VPS (2 cores, 4 GB) | ~20-30 | ~5-10 |
| Dedicated server (8 cores, 16 GB) | ~100+ | ~30-50 |

These are conservative estimates assuming worst-case concurrent audio on all bridges simultaneously. In practice, not all bridges carry audio at the same time.

## Scaling strategies

If you hit CPU limits with many XLX bridges:

- **Hardware AMBE dongles** (ThumbDV, DVSI AMBE3000) offload vocoder work from the CPU entirely
- **Distribute bridges** across multiple hosts, each connecting to the same SVXReflector
- **Vertical scaling** — a faster CPU directly increases the number of concurrent XLX audio streams
- **Stagger talkgroups** — bridges on different TGs are unlikely to all carry audio simultaneously
