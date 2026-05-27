FROM golang:1.22-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    libopus-dev pkg-config ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=1 go build -tags nolibopusfile -o mumble_bridge .

# --- Runtime stage ---
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libopus0 ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/mumble_bridge /usr/local/bin/mumble_bridge

ENTRYPOINT ["mumble_bridge"]
