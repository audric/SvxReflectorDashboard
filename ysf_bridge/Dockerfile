FROM golang:1.22-bookworm AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    cmake g++ libopus-dev libopusfile-dev pkg-config git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Build MBEVocoder from DroidStar (DMR AMBE+2 2450x1150 encode + decode)
RUN git clone --depth 1 https://github.com/nostar/DroidStar.git /tmp/droidstar \
    && cd /tmp/droidstar \
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
RUN CGO_ENABLED=1 go build -o ysf_bridge .

# --- Runtime stage ---
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libopus0 libopusfile0 ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/ysf_bridge /usr/local/bin/ysf_bridge

ENTRYPOINT ["ysf_bridge"]
