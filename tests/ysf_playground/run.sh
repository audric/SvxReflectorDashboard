#!/usr/bin/env bash
# Brings up the YSF debug playground, runs the loopback test, prints results.
# See README.md for what passes and fails mean.
set -euo pipefail
cd "$(dirname "$0")"

mkdir -p fixtures out pcap

if [[ ! -f fixtures/test.wav ]]; then
  echo "==> Generating fixtures/test.wav"
  (cd gen_fixture && go run . -out ../fixtures/test.wav)
fi

echo "==> Starting parrot + pcap"
docker compose up -d --build ysfparrot pcap
sleep 2

echo "==> Building loopback image"
docker compose build ysf_loopback

echo "==> Running loopback test"
set +e
docker compose run --rm ysf_loopback
TEST_EXIT=$?
set -e

echo
echo "==> Outputs"
ls -la out/ pcap/ 2>/dev/null || true

echo
if [[ $TEST_EXIT -eq 0 ]]; then
  echo "PASS — out/result.wav should be audible (degraded but recognizable)."
  echo "      Open pcap/ysf.pcap in Wireshark to see the YSF traffic."
else
  echo "FAIL — exit $TEST_EXIT. See test output above and pcap/ysf.pcap for diagnosis."
fi
echo
echo "When done: ./shutdown.sh  (regular 'docker compose down' may fail under rootless docker)"
exit $TEST_EXIT
