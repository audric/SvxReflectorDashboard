# Troubleshooting

## Common issues

### Port 3000 already in use

Change the host port mapping in `docker-compose.yml`:

```yaml
ports:
  - "3001:3000"  # use port 3001 on the host
```

Or stop the conflicting service.

### Database permission errors

Recreate the Docker volume:

```bash
docker compose down -v
docker compose run --rm web ./bin/rails db:prepare
docker compose up -d
```

**Warning:** `down -v` deletes the database volume and all stored events.

### Dashboard shows no nodes

1. Verify `REFLECTOR_STATUS_URL` is correct in `.env`
2. Check that the URL is reachable from inside the container:
   ```bash
   docker compose exec web curl -s "$REFLECTOR_STATUS_URL" | head
   ```
3. Check updater logs for errors:
   ```bash
   docker compose logs -f updater
   ```

### WebSocket updates not working

1. Verify Redis is running: `docker compose ps`
2. Check `REDIS_URL` matches between services
3. Look for ActionCable errors in browser developer console

### No audio when tuning in

1. Check audio bridge logs:
   ```bash
   docker compose logs -f audio_bridge
   ```
2. Verify `REFLECTOR_HOST` is reachable from the bridge container
3. Look for "Connect failed" or "auth failed" messages
4. Ensure the user has **monitor** permission (admin panel → Users)

### Push-to-Talk button not showing

PTT requires all of these:
- **HTTPS** (secure context) — `getUserMedia` is blocked on plain HTTP (except `localhost`)
- **Transmit permission** granted by an admin
- **Browser support** for `AudioEncoder` and `MediaStreamTrackProcessor`

### PTT not transmitting audio

1. Check browser console for microphone errors
2. Ensure a microphone is connected and permission is granted
3. Check audio bridge logs for "SendTalkerStart" / "SendAudio" messages
4. Verify the reflector accepts the auth key

### Web listener not appearing on the map

- The browser must grant geolocation permission
- The marker appears after the next poll cycle (up to 1 second)
- If geolocation is denied, the node card still works but no map marker is placed

### Caddy cannot bind to port 80/443 (rootless Docker)

If you see an error like:

```
cannot expose privileged port 80 … listen tcp4 0.0.0.0:80: bind: permission denied
```

Rootless Docker cannot bind to ports below 1024 by default. Allow it:

```bash
# Apply immediately
sudo sysctl -w net.ipv4.ip_unprivileged_port_start=80

# Make it permanent
echo 'net.ipv4.ip_unprivileged_port_start=80' | sudo tee -a /etc/sysctl.conf
```

This lets unprivileged containers bind to ports 80 and 443 for HTTP/HTTPS.

### System Info page shows no Docker services (rootless Docker)

The Docker socket path differs on rootless setups. Set `DOCKER_SOCK` in your `.env`:

```
DOCKER_SOCK=/run/user/1000/docker.sock
```

On standard (rootful) Docker this is not needed — the default `/var/run/docker.sock` is used.

## Viewing logs

```bash
# All services
docker compose logs -f

# Specific service
docker compose logs -f web
docker compose logs -f updater
docker compose logs -f audio_bridge

# Last 100 lines only
docker compose logs --tail=100 audio_bridge
```

## Resetting everything

```bash
docker compose down -v          # Stop and delete volumes
docker compose build --no-cache # Rebuild from scratch
docker compose up -d
docker compose run --rm web ./bin/rails db:prepare
```
