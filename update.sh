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
  echo "[1/5] Pulling latest code..."
  git pull --ff-only
else
  echo "[1/5] Code already up to date (fresh clone)."
fi

# Pull latest Docker images from GHCR (dashboard + reflector)
echo ""
echo "[2/5] Pulling Docker images..."
docker compose pull

# Rebuild reflector image if it has a remote build context
echo ""
echo "[3/5] Updating reflector..."
docker compose build --pull svxreflector

# Start/restart services (recreates containers whose images changed)
echo ""
echo "[4/5] Starting services..."
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
  echo "[5/5] Initializing database..."
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
  echo "[5/5] Running database migrations..."
  docker compose exec -T web bin/rails db:migrate 2>/dev/null || true
  echo ""
  echo "=== Update complete ==="
fi

echo ""
echo "Bridge containers are managed from /admin/bridges."
echo ""
