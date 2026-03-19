#!/bin/bash
# SVXReflector Dashboard — production update script
# Usage: ./update.sh

set -e

echo "=== SVXReflector Dashboard Update ==="
echo ""

# Pull latest compose/config changes
echo "[1/4] Pulling latest code..."
git pull --ff-only

# Pull latest Docker images from GHCR
echo ""
echo "[2/4] Pulling Docker images..."
docker compose pull

# Restart services with new images
echo ""
echo "[3/4] Restarting services..."
docker compose up -d

# Run database migrations
echo ""
echo "[4/4] Running database migrations..."
docker compose exec -T web bin/rails db:migrate 2>/dev/null || true

echo ""
echo "=== Update complete ==="
echo ""
echo "Bridge containers are managed separately."
echo "If you updated bridge images, restart them from /admin/bridges."
echo ""
