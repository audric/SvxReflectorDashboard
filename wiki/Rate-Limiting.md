# Rate Limiting

The dashboard exposes `/status` as a JSON mirror of the reflector's state (nodes, trunks, satellites, cluster TGs, config). Because trunk peers and external dashboards poll it regularly, the endpoint is protected by **Rack::Attack** with a three-tier policy: blacklist, trusted peers, general public.

All other endpoints (login, registration, admin, etc.) are **not** rate-limited by this mechanism. Login has a separate per-callsign throttle (5 failed attempts → 5-minute lockout, see [[User Management]]).

## How it works

Every request to `/status` is classified by source IP:

| Tier | Rule | Response |
|---|---|---|
| **Blacklist** | IP matches any entry in `rate_limit_blacklist` | `403 Forbidden` |
| **Trusted** | IP matches any entry in `rate_limit_trusted_ips` **or** any `HOST` from a configured trunk peer | 1 request per `rate_limit_trusted_rate` seconds (default **1s**) |
| **Public** | Everyone else | 1 request per `rate_limit_public_rate` seconds (default **10s**) |

Over-limit requests return `429 Too Many Requests` with a `Retry-After` header and JSON body:

```json
{"error": "Rate limit exceeded", "retry_after": 7}
```

The IP lists accept single addresses and CIDR networks (`1.2.3.4`, `10.0.0.0/8`, `192.168.1.0/24`) and hostnames (resolved once at load time). The lists are cached for 60 seconds — edits take up to a minute to apply.

Trunk peers are added to the trusted list **automatically** from `svxreflector.conf`'s `[TRUNK_*]` sections — you don't need to list them manually.

## Configuration

Go to `/admin/system_info` (reflector admin required) and edit the four fields in the **Rate Limiting** section:

| Setting | Type | Default | Notes |
|---|---|---|---|
| `rate_limit_blacklist` | IP/CIDR list | *(empty)* | Blocked entirely with 403 |
| `rate_limit_trusted_ips` | IP/CIDR list | *(empty)* | Union with trunk peer hosts |
| `rate_limit_trusted_rate` | seconds | `1` | Clamped to 1–60 |
| `rate_limit_public_rate` | seconds | `10` | Clamped to 1–300 |

Settings are stored in the `settings` table and persist across restarts.

## What happens if you don't configure it

**Rate limiting is always on** — even with no admin action, `/status` is throttled with the defaults:

- No one is blacklisted.
- Only trunk peers (derived from `[TRUNK_*]` sections of `svxreflector.conf`) are trusted; everyone else is public.
- Trusted peers get 1 request/second.
- The general public gets 1 request per 10 seconds.

So out of the box:

- External dashboards and scrapers polling `/status` are capped at **6 requests/minute per IP**. Anything more frequent receives `429`.
- Trunk peers using `STATUS_URL` polling (default 5s) stay under the trusted limit (1s) and see no throttling.
- Your own browser hitting the dashboard at normal rates is fine — the dashboard's own UI uses ActionCable/WebSocket (`/cable`), **not** `/status`, so the UI is never throttled.

You only need to edit the settings if:

- You operate a public dashboard/scraper from a known static IP and want a higher rate → add it to trusted.
- You see abuse from a specific address → add it to the blacklist.
- You want stricter or looser public limits for your deployment.

## Inspecting behavior

```bash
# Hit /status from your current IP — expect one 200 followed by 429s within the public window
curl -i https://your-host/status; curl -i https://your-host/status

# View the current settings
docker compose exec web bin/rails runner '
  %w[rate_limit_blacklist rate_limit_trusted_ips rate_limit_trusted_rate rate_limit_public_rate].each do |k|
    puts "#{k}=#{Setting.get(k).inspect}"
  end
'
```

The cache is per-worker (`MemoryStore`), so in a multi-Puma-worker setup each worker tracks its own counter. In practice with one Puma worker (the default Docker setup) this is a non-issue.

## Related

- [[Configuration]] — environment variables
- [[User Management]] — login attempt throttling (separate from Rack::Attack)
- `config/initializers/rack_attack.rb` — implementation
