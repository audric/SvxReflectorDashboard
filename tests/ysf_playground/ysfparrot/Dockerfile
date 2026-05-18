# Build YSFParrot from G4KLX YSFClients.
# Upstream takes the port as a CLI arg; no INI file.
FROM debian:bookworm-slim AS builder
RUN apt-get update && apt-get install -y --no-install-recommends \
        git build-essential ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 https://github.com/g4klx/YSFClients.git /src
WORKDIR /src/YSFParrot
RUN make

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /src/YSFParrot/YSFParrot /usr/local/bin/YSFParrot
EXPOSE 42000/udp
# Upstream YSFParrot.cpp parses only argv[1] as the port. Despite the usage
# string mentioning -d/--debug, the code rejects any arg starting with -.
ENTRYPOINT ["/usr/local/bin/YSFParrot", "42000"]
