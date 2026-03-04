# Code Map

## Rails application

### Models

| File | Purpose |
|---|---|
| `app/models/node_event.rb` | Persisted node events with scopes for analytics |
| `app/models/user.rb` | User accounts with callsign validation, roles, and permissions |
| `app/models/setting.rb` | Key-value runtime settings (poll interval, reflector URL) |

### Controllers

| File | Purpose |
|---|---|
| `app/controllers/dashboard_controller.rb` | Main dashboard, map, stats, and TG matrix views |
| `app/controllers/sessions_controller.rb` | Login and logout |
| `app/controllers/registrations_controller.rb` | New user registration |
| `app/controllers/admin/users_controller.rb` | Admin user management and approval |
| `app/controllers/admin/settings_controller.rb` | Admin runtime settings |

### Channels (ActionCable)

| File | Purpose |
|---|---|
| `app/channels/updates_channel.rb` | Streams live node updates to the dashboard |
| `app/channels/audio_channel.rb` | Audio streaming: tune-in, PTT, and TX audio relay |

### Views

| Directory | Purpose |
|---|---|
| `app/views/dashboard/` | Dashboard index, map, stats, TG matrix |
| `app/views/sessions/` | Login form |
| `app/views/registrations/` | Registration form |
| `app/views/admin/users/` | Admin user list and management |
| `app/views/admin/settings/` | Admin settings form |
| `app/views/shared/` | Navbar (includes audio player/PTT, S-meter, and spectrum analyser JavaScript) |
| `app/views/layouts/` | Application layout |

All templates use **HAML** (not ERB).

## Background services

| File | Purpose |
|---|---|
| `lib/reflector_listener.rb` | Polls reflector status API, diffs state, broadcasts updates, persists events, enriches web listener nodes |

## Audio bridge (Go)

| File | Purpose |
|---|---|
| `audio_bridge/main.go` | Entry point, Redis command subscriber, session manager |
| `audio_bridge/client.go` | TCP/UDP reflector client — handshake, heartbeats, audio I/O |
| `audio_bridge/protocol.go` | Wire format builders and parsers for SVXReflector protocol V2 |
| `audio_bridge/Dockerfile` | Multi-stage Go build for production |

## Infrastructure

| File | Purpose |
|---|---|
| `Dockerfile` | Multi-stage Rails production image |
| `docker-compose.yml` | Service definitions: web, updater, audio_bridge, redis |
| `.env.example` | Template for environment variables |
| `config/routes.rb` | URL routing |
| `config/cable.yml` | ActionCable Redis adapter config |
| `db/seeds.rb` | Default admin user seed |

## Database

SQLite with a single main table:

- **`node_events`** — indexed on `callsign`, `event_type`, `created_at`, `tg`
- **`users`** — callsign, password digest, role, permissions
- **`settings`** — key-value pairs for runtime configuration
