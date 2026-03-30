# ── Stage 1: Build PJSIP from source ──
FROM debian:bookworm-slim AS pjsip-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential wget ca-certificates pkg-config \
    libssl-dev uuid-dev libspeex-dev libspeexdsp-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
RUN wget -q https://github.com/pjsip/pjproject/archive/refs/tags/2.14.1.tar.gz \
    && tar -xzf 2.14.1.tar.gz \
    && cd pjproject-2.14.1 \
    && ./configure --disable-video --disable-libwebrtc \
       --prefix=/usr/local \
       CFLAGS="-fPIC -O2" CXXFLAGS="-fPIC -O2" \
    && make dep && make -j$(nproc) \
    && make install

# ── Stage 2: Build sip_helper (C, statically linked against PJSIP) ──
FROM debian:bookworm-slim AS c-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential pkg-config libssl-dev uuid-dev libspeex-dev libspeexdsp-dev ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=pjsip-builder /usr/local /usr/local
RUN ldconfig

WORKDIR /app
COPY sip_helper.c .

RUN gcc -O2 -o sip_helper sip_helper.c \
    $(pkg-config --cflags --static --libs libpjproject) \
    -lspeexdsp -lpthread -lm -luuid -lssl -lcrypto -lrt

# ── Stage 3: Build Go binary ──
FROM golang:1.22-bookworm AS go-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    libopus-dev libopusfile-dev pkg-config ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=1 go build -o sip_bridge .

# ── Runtime ──
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libopus0 libopusfile0 libssl3 ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=c-builder /app/sip_helper /usr/local/bin/sip_helper
COPY --from=go-builder /app/sip_bridge /usr/local/bin/sip_bridge

ENTRYPOINT ["sip_bridge"]
