#!/usr/bin/env bash
# Tears down the playground. Required as a separate script because the pcap
# container holds CAP_NET_RAW which makes `docker compose down` fail under
# rootless Docker ("permission denied" on stop). We SIGKILL the pcap PID
# from the host first, then run compose down.
set -euo pipefail
cd "$(dirname "$0")"

PCAP_PID=$(docker inspect ysf_playground_pcap --format '{{.State.Pid}}' 2>/dev/null || echo 0)
if [[ "$PCAP_PID" != "0" && -n "$PCAP_PID" ]]; then
  echo "==> SIGKILL pcap tcpdump (pid $PCAP_PID)"
  kill -9 "$PCAP_PID" 2>/dev/null || true
  sleep 1
fi

echo "==> docker compose down"
docker compose down --remove-orphans
