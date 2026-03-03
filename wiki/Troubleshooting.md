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
- The marker appears after the next poll cycle (up to 4 seconds)
- If geolocation is denied, the node card still works but no map marker is placed

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
