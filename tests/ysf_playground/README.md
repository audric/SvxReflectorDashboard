# YSF debug playground

A self-contained Docker compose stack for debugging the `ysf_bridge` Go code path in isolation, without needing a production YSF reflector. Drives the bridge's vocoder + FICH + protocol code with a known WAV, captures the round-trip output WAV, and writes a pcap of all YSF UDP traffic.

## Quick start

```bash
cd tests/ysf_playground
./run.sh
# ...inspect out/result.wav and pcap/ysf.pcap...
./shutdown.sh
```

First run builds two images (YSFParrot from G4KLX YSFClients, and the ysf_bridge test image). Subsequent runs reuse Docker cache.

`run.sh`:
1. Generates `fixtures/test.wav` if missing (1.5 s, mono 8 kHz, sine + chirp).
2. Starts `ysfparrot` and the `pcap` sidecar.
3. Builds and runs the loopback Go test against the parrot.
4. Prints PASS or FAIL with paths to outputs.

`shutdown.sh`: required teardown because the pcap container's `CAP_NET_RAW` makes regular `docker compose down` fail under rootless Docker. The script SIGKILLs the pcap PID first, then runs `compose down`.

## What it tests

```
fixtures/test.wav
  ↓ ysf_loopback (Go test): reads WAV, encodes PCM → IMBE → YSFD
  ↓ UDP → ysfparrot
  ↓ ysfparrot echoes back (after ~3-4 s internal buffer)
  ↓ ysf_loopback: receives YSFD, decodes IMBE → PCM, writes WAV
out/result.wav

In parallel:
  pcap sidecar in ysfparrot's net namespace
  → pcap/ysf.pcap (all UDP/42000 traffic, both directions)
```

The test (`ysf_bridge/loopback_test.go`) reuses the bridge's actual code — same package, same internals: `ysf_client.go`, `ysf_protocol.go`, `ysf_codec.go`, `vocoder.go`.

The test does NOT use `client.RunReader()` because that filter drops YSFD frames whose `SrcGateway` matches our own callsign (correct in production: don't process your own echo as remote traffic). The test runs its own reader on `client.conn` directly, bypassing that filter.

**It does NOT exercise the SVX reflector side** (`svxlink_client.go`, `redis_client.go`, `main.go` orchestration). Bugs in those layers won't show up here.

## Pass / fail interpretation

| Symptom | Likely cause |
|---|---|
| Test exits on YSFP timeout | Bridge can't register; YSFParrot rejected our poll (wrong magic, length, callsign padding) or unreachable |
| YSFP succeeded, no YSFD echo | Outgoing YSFD shape rejected by YSFParrot (wrong length, header bytes, malformed FICH triggers a drop) |
| YSFD echoed, parse error | Our parser stricter than YSFParrot; FICH or framing assumption mismatch |
| YSFD parsed, output PCM is silence | IMBE decoder broken or VCH bit-extraction map wrong |
| All steps work, RMS ratio < 3 % | Codec round-trip OK at framing level, wrong at content level (bit packing, frame ordering) |
| Pass | YSF half is functional in isolation; remaining bugs live in SVX-side code or prod env |

## Expected output for the default fixture

The default synthetic sine+chirp WAV is not voice-like, so AMBE+2 attenuates it dramatically. Expected:

- Output sample count: exactly 12000 (1.5 s — matches input).
- Output RMS ratio: ~10-15 % of input (synthetic input through voice codec).
- 17 echoed YSFDs received (1 HEADER + 15 COMM + 1 TERM).
- First echo latency: 3-5 s (YSFParrot's internal buffer).

For meaningful audio quality verification, drop your own voice WAV at `fixtures/test.wav` (mono 8 kHz 16-bit PCM):

```bash
sox myvoice.wav -r 8000 -c 1 -b 16 fixtures/test.wav
```

Real voice through the round-trip should produce 30-60 % RMS ratio and remain intelligible.

## Inspecting the pcap

```bash
# Textual dump
tshark -r pcap/ysf.pcap -V | head -60

# Open in Wireshark for byte-level inspection
wireshark pcap/ysf.pcap
```

What to look for:
- YSFP poll outbound: 14 bytes, magic `59 53 46 50` (`YSFP`), 10 bytes space-padded callsign.
- YSFP response inbound: 14 bytes, magic `YSFP` + name (e.g. "PARROT    ").
- YSFD frames: 155 bytes each, magic `YSFD`, 10 bytes gateway CS, 10 src, 10 dst, then 1-byte counter+EOT, then 120 bytes (5 sync + 25 FICH + 90 payload).

Compare against G4KLX YSFClients source if anything looks off.

## Caveats

**Symmetric-bug blind spot.** The loopback test imports bridge code on both ends of the loop. A bug present in both encode and decode (e.g. wrong bit order applied symmetrically) can produce a passing test while the prod bridge fails against real YSF endpoints. Mitigation: the pcap is independent ground truth — manually inspect frame sizes, magic bytes, FICH bits in Wireshark. YSFParrot also rejects malformed frames before echoing, so passing implies the wire format is at least acceptable to a known-good C++ implementation.

**Rootless Docker quirk.** The pcap container needs `cap_add: [NET_ADMIN, NET_RAW]` for tcpdump to capture. Under rootless Docker, this makes the container unstoppable via standard `docker compose down`. Use `./shutdown.sh` which sends SIGKILL to the tcpdump PID first.

**Self-echo filter bypass.** The test does NOT use `client.RunReader()` because it would drop our own echo as "self traffic". This is documented in the source and is intentional. The bypass means we're exercising the protocol/codec layers but not the production reader's full state machine.

## Cleanup

```bash
./shutdown.sh                            # stops containers, removes network
rm -rf out/ pcap/ fixtures/test.wav      # discard test artifacts
```
