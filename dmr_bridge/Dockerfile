# --- ARM builder stage: md380_helper (real MD380 firmware, runs via qemu-arm) ---
FROM debian:bookworm-slim AS armbuilder
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc-arm-linux-gnueabihf libc6-dev-armhf-cross wget ca-certificates git \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 https://github.com/nostar/md380_vocoder.git /tmp/md380voc
WORKDIR /tmp/md380voc
RUN wget -N http://www.dudetronics.com/radio/md380fw.img \
    && arm-linux-gnueabihf-objcopy \
        -I binary md380fw.img \
        --change-addresses=0x0800C000 \
        --rename-section .data=.firmware \
        --add-symbol ambe_encode_thing=0x08050d91 \
        --add-symbol ambe_unpack=0x08048c9d \
        --add-symbol ambe_decode_wav=0x08051249 \
        -O elf32-littlearm -B arm firmware.o \
    && wget -N http://www.dudetronics.com/radio/md380ram.img \
    && arm-linux-gnueabihf-objcopy \
        -I binary md380ram.img \
        --change-addresses=0x20000000 \
        --rename-section .data=.sram \
        --add-symbol ambe_inbuffer=0x20011c8e \
        --add-symbol ambe_outbuffer0=0x20011aa8 \
        --add-symbol ambe_outbuffer1=0x20011b48 \
        --add-symbol ambe_mystery=0x20011224 \
        --add-symbol ambe_en_mystery=0x2000c730 \
        --add-symbol wav_inbuffer0=0x2000de82 \
        --add-symbol wav_inbuffer1=0x2000df22 \
        --add-symbol ambe_outbuffer=0x2000dfc6 \
        -O elf32-littlearm -B arm ram.o
COPY md380_helper.c ./
RUN arm-linux-gnueabihf-gcc -static -O2 -o md380_helper \
    md380_helper.c md380_vocoder.c firmware.o ram.o \
    -Xlinker --section-start=.firmware=0x0800C000 \
    -Xlinker --section-start=.sram=0x20000000

# --- Go builder stage (x86_64) ---
FROM golang:1.22-bookworm AS builder
RUN apt-get update && apt-get install -y --no-install-recommends \
    libopus-dev libopusfile-dev pkg-config ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=1 go build -o dmr_bridge .

# --- Runtime stage ---
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    libopus0 libopusfile0 ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
# DMR radio ID -> callsign lookup table, baked in at build time. Update by
# rebuilding this image (docker compose build dmr_bridge).
RUN curl -fsSL https://radioid.net/static/user.csv -o /usr/local/share/dmrids.csv
COPY --from=armbuilder /tmp/md380voc/md380_helper /usr/local/bin/md380_helper
COPY --from=builder /app/dmr_bridge /usr/local/bin/dmr_bridge
ENTRYPOINT ["dmr_bridge"]
