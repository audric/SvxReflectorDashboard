# SVX Dashboard Wiki

Welcome to the SVX Dashboard wiki — the complete reference for deploying, using, and developing the SVX Reflector Dashboard.

## Quick links

- **[[Getting Started]]** — clone, configure, and run in 5 minutes
- **[[Architecture]]** — services, data flow, and audio path
- **[[Configuration]]** — environment variables and settings
- **[[User Management]]** — registration, approval, and permissions
- **[[Audio Bridge]]** — the Go service that connects browsers to the reflector
- **[[Reflector Protocol]]** — SVXReflector protocol V2 wire format
- **[[Troubleshooting]]** — common issues and fixes
- **[[Code Map]]** — where everything lives in the codebase

## What is SVX Dashboard?

A Rails web application for monitoring amateur radio [SVXReflector](https://www.svxlink.org/) node activity in real time. It polls a reflector's HTTP status API, persists node events in SQLite, and pushes live updates to browsers via ActionCable/Redis.

Registered users can tune in to talkgroups and transmit audio directly from their browser.

### Features

- **Live dashboard** — node grid with color-coded status, signal levels, squelch indicators, and a scrolling activity log
- **Map** — interactive Leaflet.js map with per-node popups and multiple tile layers
- **Stats** — historical analytics: top talkers, top talkgroups, node type distribution, signal strength
- **TG Matrix** — CTCSS tone-to-talkgroup mapping table
- **Web listener** — tune in to any talkgroup and receive live Opus audio in the browser
- **Push-to-Talk** — transmit from the browser microphone (requires HTTPS)
- **User accounts** — registration with callsign validation, admin approval, per-user permissions
- **Admin panel** — user management, registration approval, reflector settings

### Stack

Ruby 3.2 · Rails 7.1 · Go · SQLite · Redis · Hotwire (Turbo + Stimulus) · HAML · Bootstrap 5 · Leaflet.js
