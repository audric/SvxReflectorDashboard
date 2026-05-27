# Mumble (Murmur) server + a ZeroC Ice management client, so the dashboard can
# register/update users and set ACL groups on the LIVE server (no restart),
# via `docker exec` of mumble_ice.py talking to Ice on 127.0.0.1:6502.

# ── builder: compile zeroc-ice (no cp312 wheel) + fetch the Murmur slice ──
FROM mumblevoip/mumble-server:latest AS builder
USER root
ENV MAKEFLAGS="-j4"
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       python3 python3-pip python3-dev build-essential libssl-dev libbz2-dev curl ca-certificates \
    && pip3 install --no-cache-dir --break-system-packages zeroc-ice \
    # Murmur Slice definition (interface is stable across 1.5.x). Pinned for
    # reproducible builds.
    && curl -fsSL -o /usr/local/share/MumbleServer.ice \
       https://raw.githubusercontent.com/mumble-voip/mumble/v1.5.735/src/murmur/MumbleServer.ice

# ── final: official image + python3 runtime + the compiled Ice package only ──
FROM mumblevoip/mumble-server:latest
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 libssl3 libbz2-1.0 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /usr/local/lib/python3.12/dist-packages /usr/local/lib/python3.12/dist-packages
COPY --from=builder /usr/local/share/MumbleServer.ice /usr/local/share/MumbleServer.ice
COPY mumble_ice.py /usr/local/bin/mumble_ice.py
RUN chmod +x /usr/local/bin/mumble_ice.py
# ENTRYPOINT/CMD inherited from the base image (entrypoint drops to the murmur uid).
