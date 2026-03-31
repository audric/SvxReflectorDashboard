#!/bin/bash
# SVXReflector Dashboard — install & update script
#
# First install:  git clone <repo> && cd <repo> && cp .env.example .env && nano .env && ./update.sh
# Updates:        ./update.sh

set -e

echo "=== SVXReflector Dashboard ==="
echo ""

# Detect first install vs update
FIRST_INSTALL=false
if ! docker compose ps --quiet web 2>/dev/null | grep -q .; then
  FIRST_INSTALL=true
  echo "First install detected."
  echo ""
  if [ ! -f .env ]; then
    echo "ERROR: .env file not found. Copy .env.example and configure it first:"
    echo "  cp .env.example .env"
    echo "  nano .env"
    exit 1
  fi
fi

# Pull latest code (skip on first install — just cloned)
if [ "$FIRST_INSTALL" = false ]; then
  echo "[1/4] Pulling latest code..."
  git pull --ff-only
else
  echo "[1/4] Code already up to date (fresh clone)."
fi

# Pull latest Docker images from GHCR (dashboard + reflector + bridges)
echo ""
echo "[2/4] Pulling Docker images..."
docker compose pull || echo "  Warning: some images failed to pull (will use cached)"
# Pull bridge images (managed dynamically, not in docker-compose.yml)
BRIDGE_IMAGES="
ghcr.io/audric/svxreflectordashboard-xlx-bridge
ghcr.io/audric/svxreflectordashboard-dmr-bridge
ghcr.io/audric/svxreflectordashboard-ysf-bridge
ghcr.io/audric/svxreflectordashboard-allstar-bridge
ghcr.io/audric/svxreflectordashboard-zello-bridge
ghcr.io/audric/svxreflectordashboard-iax-bridge
ghcr.io/audric/svxreflectordashboard-sip-bridge
"
for img in $BRIDGE_IMAGES; do
  echo "  Pulling $img..."
  docker pull -q "$img:latest" 2>/dev/null || true
done

# Start/restart services (recreates containers whose images changed)
echo ""
echo "[3/4] Starting services..."
docker compose up -d

# Wait for web to be ready
echo ""
echo -n "Waiting for web service..."
for i in $(seq 1 30); do
  if docker compose exec -T web bin/rails runner "puts :ok" 2>/dev/null | grep -q ok; then
    echo " ready."
    break
  fi
  echo -n "."
  sleep 2
done

# Database setup
echo ""
if [ "$FIRST_INSTALL" = true ]; then
  echo "[4/4] Initializing database..."
  docker compose exec -T web bin/rails db:prepare
  echo ""
  echo "=== Installation complete ==="
  echo ""
  echo "Default admin account:"
  echo "  Callsign: ADM1N"
  echo "  Password: changeme"
  echo ""
  echo "Change this password immediately after first login."
else
  echo "[4/4] Running database migrations..."
  docker compose exec -T web bin/rails db:migrate 2>/dev/null || true

  # Restart running bridge containers so they pick up new images
  echo ""
  echo "Restarting bridge containers..."
  BRIDGE_CONTAINERS=$(docker ps --format '{{.Names}}' 2>/dev/null | grep -E '^(svxlink|xlx|dmr|ysf|allstar|zello|iax|sip)-bridge-[0-9]+$' || true)
  if [ -n "$BRIDGE_CONTAINERS" ]; then
    for cname in $BRIDGE_CONTAINERS; do
      echo "  Restarting $cname..."
      docker stop "$cname" >/dev/null 2>&1 || true
      docker rm "$cname" >/dev/null 2>&1 || true
    done
    sleep 2
    docker compose exec -T web bin/rails runner 'Rails.logger = Logger.new("/dev/null"); Admin::BridgeController.restart_enabled_bridges'
  else
    echo "  No bridge containers running."
  fi

  echo ""
  echo "=== Update complete ==="
fi

echo ""
echo "Bridge containers are managed from /admin/bridges."
echo ""
