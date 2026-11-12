# --- ARM builder stage: md380_helper ---
# A static 32-bit ARM helper that runs the MD380 firmware vocoder. The
# firmware itself is NOT downloaded or included: the helper maps image
# files supplied by the operator at run time, and the firmware entry
# points are resolved here as absolute symbols. The helper is the same
# whatever the image platform, so it is cross-compiled on the build host.
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS armbuilder
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc-arm-linux-gnueabihf libc6-dev-armhf-cross ca-certificates git \
    && rm -rf /var/lib/apt/lists/*
# Pinned to a known commit so upstream changes cannot break or alter the build.
RUN mkdir /tmp/md380voc && cd /tmp/md380voc && git init -q \
    && git fetch -q --depth 1 https://github.com/nostar/md380_vocoder.git 0ebd2761d441db354f859280873d4286856f3e73 \
    && git checkout -q FETCH_HEAD
WORKDIR /tmp/md380voc
COPY md380_helper.c ./
RUN arm-linux-gnueabihf-gcc -static -O2 -o md380_helper md380_helper.c md380_vocoder.c \
    -Wl,--defsym=ambe_encode_thing=0x08050d91 \
    -Wl,--defsym=ambe_unpack=0x08048c9d \
    -Wl,--defsym=ambe_decode_wav=0x08051249 \
    -Wl,--defsym=ambe_inbuffer=0x20011c8e \
    -Wl,--defsym=ambe_outbuffer0=0x20011aa8 \
    -Wl,--defsym=ambe_outbuffer1=0x20011b48 \
    -Wl,--defsym=ambe_mystery=0x20011224 \
    -Wl,--defsym=ambe_en_mystery=0x2000c730 \
    -Wl,--defsym=wav_inbuffer0=0x2000de82 \
    -Wl,--defsym=wav_inbuffer1=0x2000df22 \
    -Wl,--defsym=ambe_outbuffer=0x2000dfc6

# --- qemu stage: user-mode ARM emulator for the image platform ---
# Shipping qemu-arm-static in the image means the host needs no qemu or
# binfmt_misc setup. Only this one binary is copied to the runtime image.
FROM debian:bookworm-slim AS qemu
RUN apt-get update && apt-get install -y --no-install-recommends qemu-user-static \
    && rm -rf /var/lib/apt/lists/*

# --- Go builder stage ---
FROM golang:1.22-bookworm AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    cmake g++ libopus-dev libopusfile-dev pkg-config git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Build MBEVocoder from DroidStar (DMR AMBE+2 2450x1150 encode + decode)
# Pinned: later upstream commits removed the mbelib sources used below.
RUN git init -q /tmp/droidstar \
    && cd /tmp/droidstar \
    && git fetch -q --depth 1 https://github.com/nostar/DroidStar.git 28b10be62b4c7b53c6720a0f083e8ee7a450effb \
    && git checkout -q FETCH_HEAD \
    && gcc -c -O2 -Imbe \
        mbe/mbelib.c mbe/ambe3600x2400.c mbe/ambe3600x2450.c mbe/ecc.c \
    && g++ -c -O2 -I. -Imbe -Iimbe_vocoder \
        mbe/mbevocoder.cpp \
        imbe_vocoder/*.cc \
    && ar rcs libmbevocoder.a *.o \
    && cp libmbevocoder.a /usr/local/lib/ \
    && cp mbe/mbevocoder.h mbe/mbelib.h mbe/mbelib_parms.h /usr/local/include/ \
    && mkdir -p /usr/local/include/imbe_vocoder \
    && cp imbe_vocoder/imbe_vocoder.h imbe_vocoder/imbe_vocoder_api.h \
       imbe_vocoder/imbe_vocoder_impl.h imbe_vocoder/imbe.h /usr/local/include/imbe_vocoder/ \
    && cp mbe/vocoder_tables.h mbe/ambe3600x2400_const.h mbe/ambe3600x2450_const.h \
       mbe/ecc_const.h mbe/mbelib_const.h mbe/mbevocoder_api.h /usr/local/include/ \
    && rm -rf /tmp/droidstar

WORKDIR /app

# Build C wrapper for MBEVocoder (C++ → C linkage)
COPY vocoder_wrapper.cpp ./
RUN g++ -c -O2 -I/usr/local/include vocoder_wrapper.cpp -o vocoder_wrapper.o \
    && ar rcs /usr/local/lib/libvocoder_wrapper.a vocoder_wrapper.o \
    && rm vocoder_wrapper.o

# Copy go module files first for caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source
COPY *.go ./

# Build with CGo enabled (needed for MBEVocoder and opus)
RUN CGO_ENABLED=1 go build -o dmr_bridge .

# --- Runtime stage ---
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libopus0 libopusfile0 ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=qemu /usr/bin/qemu-arm-static /usr/local/bin/qemu-arm-static
COPY --from=armbuilder /tmp/md380voc/md380_helper /usr/local/bin/md380_helper
COPY --from=builder /app/dmr_bridge /usr/local/bin/dmr_bridge

ENTRYPOINT ["dmr_bridge"]
