#!/usr/bin/env bash
set -e

echo "Creating new Rails app in /app..."
cd /app
rails new . --skip-bundle --skip-test --skip-system-test --asset-pipeline=sprockets -d sqlite3

echo "Adding gems..."
cat >> Gemfile <<'GEM'
gem 'redis'
GEM

bundle install

echo "Generating Dashboard controller..."
bundle exec rails generate controller Dashboard index --skip-routes

# ── DashboardController ──────────────────────────────────────────────────────
cat > app/controllers/dashboard_controller.rb <<'RUBY'
require 'net/http'
require 'uri'
require 'json'

class DashboardController < ApplicationController
  def index
    @status_url     = ENV.fetch('REFLECTOR_STATUS_URL', 'http://213.254.10.33:8181/status')
    @reflector_host = ENV.fetch('REFLECTOR_HOST', '213.254.10.33')
    @reflector_port = ENV.fetch('REFLECTOR_PORT', '5300')
    begin
      res    = Net::HTTP.get_response(URI.parse(@status_url))
      parsed = JSON.parse(res.body)
      @nodes = parsed.fetch('nodes', {})
    rescue => e
      @nodes       = {}
      @fetch_error = e.message
    end
  end
end
RUBY

# ── Routes ───────────────────────────────────────────────────────────────────
sed -i "s|Rails.application.routes.draw do|Rails.application.routes.draw do\n  root 'dashboard#index'|" config/routes.rb

# ── UpdatesChannel ───────────────────────────────────────────────────────────
cat > app/channels/updates_channel.rb <<'RUBY'
class UpdatesChannel < ApplicationCable::Channel
  def subscribed
    stream_from 'updates'
  end
end
RUBY

# ── ReflectorListener ────────────────────────────────────────────────────────
cat > lib/reflector_listener.rb <<'RUBY'
require 'socket'
require 'json'

class ReflectorListener
  def self.start(host = nil, port = nil)
    host ||= ENV.fetch('REFLECTOR_HOST', '213.254.10.33')
    port ||= ENV.fetch('REFLECTOR_PORT', '5300').to_i
    Thread.new do
      loop do
        socket = nil
        begin
          STDERR.puts "[Listener] Connecting to #{host}:#{port}..."
          socket = TCPSocket.new(host, port)
          STDERR.puts "[Listener] Connected."
          while (line = socket.gets)
            line = line.chomp
            next if line.empty?
            data = JSON.parse(line) rescue { raw: line }
            data['_ts'] = Time.now.iso8601
            ActionCable.server.broadcast('updates', data)
          end
          STDERR.puts "[Listener] Connection closed, reconnecting in 5s..."
        rescue => e
          STDERR.puts "[Listener] Error: #{e.message}, reconnecting in 5s..."
        ensure
          socket&.close rescue nil
        end
        sleep 5
      end
    end
  end
end
RUBY

# ── ActionCable / Redis ───────────────────────────────────────────────────────
cat > config/cable.yml <<'YAML'
development:
  adapter: redis
  url: <%= ENV.fetch("REDIS_URL", "redis://redis:6379/1") %>

test:
  adapter: async

production:
  adapter: redis
  url: <%= ENV.fetch("REDIS_URL") %>
YAML

# ── Dashboard View ────────────────────────────────────────────────────────────
cat > app/views/dashboard/index.html.erb <<'ENDVIEW'
<!DOCTYPE html>
<html lang="en" data-bs-theme="dark">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>SVXReflector Dashboard</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css">
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bootstrap-icons@1.11.3/font/bootstrap-icons.min.css">
  <style>
    body { background: #0d1117; }

    .node-card {
      border: 1px solid #30363d;
      border-left-width: 4px;
      transition: border-color .3s, box-shadow .3s;
    }
    .node-idle    { border-left-color: #6e7681; }
    .node-active  { border-left-color: #1f6feb; }
    .node-talking {
      border-left-color: #3fb950;
      box-shadow: 0 0 0 1px rgba(63,185,80,.3), 0 0 16px rgba(63,185,80,.15);
      animation: glow-pulse 1.4s ease-in-out infinite;
    }
    @keyframes glow-pulse {
      0%,100% { box-shadow: 0 0 0 1px rgba(63,185,80,.3), 0 0 12px rgba(63,185,80,.1); }
      50%     { box-shadow: 0 0 0 2px rgba(63,185,80,.5), 0 0 24px rgba(63,185,80,.3); }
    }

    .node-card .card-header { background: #161b22; border-bottom: 1px solid #21262d; }
    .node-card .card-footer { background: #0d1117; border-top:    1px solid #21262d; font-size: .72rem; }

    #activity-log { max-height: 540px; overflow-y: auto; scrollbar-width: thin; }
    .activity-item { font-size: .82rem; border-color: #21262d !important; }
    .activity-item:hover { background: #161b22 !important; }

    .ws-dot { width: 8px; height: 8px; border-radius: 50%; display: inline-block; vertical-align: middle; }
    .ws-connected    { background: #3fb950; }
    .ws-disconnected { background: #f85149; }
    .ws-connecting   { background: #d29922; animation: blink 1s step-start infinite; }
    @keyframes blink { 50% { opacity: .2; } }

    .tg-pill {
      font-size: .72rem; padding: .1em .5em;
      background: #21262d; border: 1px solid #30363d;
      border-radius: 2em; color: #8b949e;
    }
    .tg-pill.active { background: #1f3a5f; border-color: #1f6feb; color: #79c0ff; }

    .freq-label { font-size: .78rem; font-family: monospace; }

    @keyframes flash-update {
      0%   { background-color: rgba(210,153,34,.22); }
      100% { background-color: transparent; }
    }
    .flash { animation: flash-update .8s ease-out; }
  </style>
</head>
<body>

<%# ── Navbar ──────────────────────────────────────────────────────────────── %>
<nav class="navbar bg-dark border-bottom border-secondary-subtle sticky-top mb-4 py-2">
  <div class="container-fluid">
    <span class="navbar-brand fw-bold text-light mb-0">
      <i class="bi bi-broadcast-pin me-2 text-success"></i>SVXReflector
    </span>
    <div class="d-flex align-items-center gap-3">
      <span class="text-secondary small font-monospace">
        <%= @reflector_host %>:<%= @reflector_port %>
      </span>
      <span class="d-flex align-items-center gap-1">
        <span id="ws-dot" class="ws-dot ws-connecting"></span>
        <span id="ws-label" class="text-secondary small">Connecting…</span>
      </span>
    </div>
  </div>
</nav>

<div class="container-fluid px-3 px-md-4">

  <%# ── Fetch error ────────────────────────────────────────────────────────── %>
  <% if @fetch_error %>
    <div class="alert alert-warning d-flex align-items-center mb-4" role="alert">
      <i class="bi bi-exclamation-triangle-fill me-2 flex-shrink-0"></i>
      Could not load status from <code class="mx-1"><%= @status_url %></code>: <%= @fetch_error %>
    </div>
  <% end %>

  <%# ── Stats row ──────────────────────────────────────────────────────────── %>
  <div class="row g-3 mb-4">
    <%
      stats = [
        { id: 'stat-total',   icon: 'bi-diagram-3',   color: 'text-light',   label: 'Nodes',        val: @nodes.size },
        { id: 'stat-active',  icon: 'bi-wifi',         color: 'text-primary', label: 'Active',       val: @nodes.count { |_, n| n['tg'].to_i != 0 } },
        { id: 'stat-talking', icon: 'bi-mic-fill',     color: 'text-success', label: 'Talking',      val: @nodes.count { |_, n| n['isTalker'] } },
        { id: 'stat-updates', icon: 'bi-arrow-repeat', color: 'text-warning', label: 'Live Updates', val: 0 },
      ]
    %>
    <% stats.each do |s| %>
      <div class="col-6 col-md-3">
        <div class="card border-secondary-subtle h-100">
          <div class="card-body text-center py-3">
            <div class="display-6 fw-bold <%= s[:color] %>" id="<%= s[:id] %>"><%= s[:val] %></div>
            <div class="text-secondary small mt-1">
              <i class="bi <%= s[:icon] %> me-1"></i><%= s[:label] %>
            </div>
          </div>
        </div>
      </div>
    <% end %>
  </div>

  <%# ── Main: node grid + activity log ─────────────────────────────────────── %>
  <div class="row g-4">

    <%# Node grid %>
    <div class="col-12 col-xl-8">
      <div class="d-flex align-items-center mb-3 gap-2">
        <span class="text-secondary text-uppercase fw-semibold small">
          <i class="bi bi-hdd-network me-1"></i>Connected Nodes
        </span>
        <span class="badge bg-secondary" id="nodes-count">
          <%= @nodes.reject { |_, n| n['hidden'] }.size %>
        </span>
      </div>

      <div class="row g-3" id="nodes-grid">
        <%
          visible = @nodes.reject { |_, n| n['hidden'] }
          sorted  = visible.sort_by { |cs, n|
            [ n['isTalker'] ? 0 : (n['tg'].to_i != 0 ? 1 : 2), cs ]
          }
        %>
        <% sorted.each do |callsign, node| %>
          <%
            tg         = node['tg'].to_i
            is_talker  = node['isTalker']
            st_cls     = is_talker ? 'node-talking' : (tg != 0 ? 'node-active' : 'node-idle')
            st_txt     = is_talker ? "TALKING \u00B7 TG #{tg}" : (tg != 0 ? "TG #{tg}" : 'IDLE')
            st_col     = is_talker ? 'success' : (tg != 0 ? 'primary' : 'secondary')
            qth        = node.dig('qth', 0) || {}
            rx_freq    = qth['rx']&.values&.first&.dig('freq')
            tx_freq    = qth['tx']&.values&.first&.dig('freq')
            locator    = qth.dig('pos', 'loc')
            node_icon  = { 'repeater' => 'bi-arrow-repeat',
                           'bridge'   => 'bi-link-45deg',
                           'simplex'  => 'bi-broadcast' }.fetch(node['nodeClass'].to_s, 'bi-broadcast')
          %>
          <div class="col-12 col-sm-6 col-xxl-4" id="node-col-<%= callsign %>">
            <div class="card node-card h-100 <%= st_cls %>"
                 id="node-<%= callsign %>"
                 data-callsign="<%= callsign %>">

              <div class="card-header d-flex align-items-center justify-content-between py-2 px-3">
                <span class="fw-bold font-monospace fs-6 text-light"><%= callsign %></span>
                <span class="badge text-bg-<%= st_col %> small" id="node-<%= callsign %>-status">
                  <%= st_txt %>
                </span>
              </div>

              <div class="card-body py-2 px-3 small text-secondary">

                <%# Class + Location + Locator %>
                <div class="mb-2 d-flex flex-wrap align-items-center gap-2">
                  <span class="text-capitalize">
                    <i class="bi <%= node_icon %> me-1"></i><%= node['nodeClass'] %>
                  </span>
                  <% if node['nodeLocation'].present? %>
                    <span class="text-truncate" title="<%= node['nodeLocation'] %>">
                      <i class="bi bi-geo-alt me-1"></i><%= node['nodeLocation'] %>
                    </span>
                  <% end %>
                  <% if locator.present? %>
                    <span class="badge bg-dark border border-secondary text-secondary"
                          title="Maidenhead locator"><%= locator %></span>
                  <% end %>
                </div>

                <%# Frequencies %>
                <% if rx_freq.to_f > 0 || tx_freq.to_f > 0 %>
                  <div class="mb-2 d-flex gap-3">
                    <% if rx_freq.to_f > 0 %>
                      <span class="freq-label">
                        <span class="text-secondary">RX</span>
                        <span class="text-light ms-1"><%= "%.4f" % rx_freq %> MHz</span>
                      </span>
                    <% end %>
                    <% if tx_freq.to_f > 0 %>
                      <span class="freq-label">
                        <span class="text-secondary">TX</span>
                        <span class="text-light ms-1"><%= "%.4f" % tx_freq %> MHz</span>
                      </span>
                    <% end %>
                  </div>
                <% end %>

                <%# Monitored TGs %>
                <% if node['monitoredTGs']&.any? %>
                  <div class="mb-2 d-flex flex-wrap gap-1 align-items-center">
                    <span class="me-1">TGs:</span>
                    <% node['monitoredTGs'].each do |tg_num| %>
                      <span class="tg-pill <%= tg_num == tg && tg != 0 ? 'active' : '' %>"
                            id="node-<%= callsign %>-tgpill-<%= tg_num %>">
                        <%= tg_num %>
                      </span>
                    <% end %>
                  </div>
                <% end %>

                <%# Sysop %>
                <% if node['sysop'].present? %>
                  <div><i class="bi bi-person me-1"></i><%= node['sysop'] %></div>
                <% end %>

              </div>

              <div class="card-footer d-flex justify-content-between px-3 py-1">
                <span class="text-secondary">
                  <%= [node['sw'], node['swVer']].compact.join(' ') %>
                </span>
                <span class="text-secondary" id="node-<%= callsign %>-updated"></span>
              </div>

            </div>
          </div>
        <% end %>

        <% if visible.empty? %>
          <div class="col-12 text-center text-secondary py-5">
            <i class="bi bi-hdd-network display-4 d-block mb-2"></i>
            No nodes currently connected.
          </div>
        <% end %>
      </div><!-- #nodes-grid -->
    </div><!-- node grid col -->

    <%# Activity log %>
    <div class="col-12 col-xl-4">
      <div class="d-flex align-items-center mb-3 gap-2">
        <span class="text-secondary text-uppercase fw-semibold small">
          <i class="bi bi-activity me-1"></i>Activity Log
        </span>
        <span class="badge bg-secondary" id="log-count">0</span>
      </div>
      <div id="activity-log" class="border border-secondary-subtle rounded">
        <div id="log-placeholder" class="text-center text-secondary py-5 small">
          <i class="bi bi-hourglass-split d-block mb-2 fs-4"></i>
          Waiting for live updates…
        </div>
      </div>
    </div>

  </div><!-- .row main -->
</div><!-- .container-fluid -->

<script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/js/bootstrap.bundle.min.js"></script>
<script>
(function () {
  'use strict';

  // Initial state cloned from server render
  var nodes = <%= @nodes.to_json.html_safe %>;
  var updateCount = 0;
  var logCount    = 0;
  var MAX_LOG     = 60;

  // ── Utilities ──────────────────────────────────────────────────────────────
  function setText(id, val) {
    var el = document.getElementById(id);
    if (el) el.textContent = val;
  }

  function esc(s) {
    return String(s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;')
      .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  // ── Stats ──────────────────────────────────────────────────────────────────
  function refreshStats() {
    var vals = Object.values(nodes);
    setText('stat-total',   vals.length);
    setText('stat-active',  vals.filter(function(n){ return n.tg && n.tg !== 0; }).length);
    setText('stat-talking', vals.filter(function(n){ return n.isTalker; }).length);
    setText('nodes-count',  vals.filter(function(n){ return !n.hidden; }).length);
  }

  // ── Node card update ───────────────────────────────────────────────────────
  function updateCard(callsign, patch) {
    var card = document.getElementById('node-' + callsign);
    if (!card) return;

    if (!nodes[callsign]) nodes[callsign] = {};
    Object.assign(nodes[callsign], patch);
    var n = nodes[callsign];

    var tg       = (n.tg !== undefined) ? n.tg : 0;
    var isTalker = !!n.isTalker;

    card.classList.remove('node-idle', 'node-active', 'node-talking');
    card.classList.add(isTalker ? 'node-talking' : (tg !== 0 ? 'node-active' : 'node-idle'));

    var badge = document.getElementById('node-' + callsign + '-status');
    if (badge) {
      badge.className  = 'badge small text-bg-' + (isTalker ? 'success' : (tg !== 0 ? 'primary' : 'secondary'));
      badge.textContent = isTalker
        ? ('TALKING \u00B7 TG ' + tg)
        : (tg !== 0 ? 'TG ' + tg : 'IDLE');
    }

    // Highlight active TG pill
    if (n.monitoredTGs) {
      n.monitoredTGs.forEach(function (tgNum) {
        var pill = document.getElementById('node-' + callsign + '-tgpill-' + tgNum);
        if (pill) pill.className = 'tg-pill' + (tgNum === tg && tg !== 0 ? ' active' : '');
      });
    }

    var updEl = document.getElementById('node-' + callsign + '-updated');
    if (updEl) updEl.textContent = new Date().toLocaleTimeString();

    // Brief flash
    card.classList.remove('flash');
    void card.offsetWidth;
    card.classList.add('flash');
  }

  // ── Activity log ───────────────────────────────────────────────────────────
  function addLog(msg) {
    var placeholder = document.getElementById('log-placeholder');
    if (placeholder) placeholder.remove();

    logCount++;
    setText('log-count', logCount);

    var log  = document.getElementById('activity-log');
    var item = document.createElement('div');
    item.className = 'activity-item list-group-item list-group-item-action px-3 py-2';

    var callsign = msg.callsign ? esc(msg.callsign) : '';
    item.innerHTML =
      '<span class="text-secondary me-2">' + new Date().toLocaleTimeString() + '</span>' +
      (callsign ? '<strong class="font-monospace me-1 text-light">' + callsign + '</strong>' : '') +
      fmtMsg(msg);

    log.insertBefore(item, log.firstChild);
    while (log.children.length > MAX_LOG) log.removeChild(log.lastChild);
  }

  function fmtMsg(msg) {
    if (msg.raw)
      return '<span class="text-warning font-monospace">' + esc(msg.raw.substring(0, 80)) + '</span>';
    if (msg.isTalker !== undefined)
      return msg.isTalker
        ? '<span class="text-success"><i class="bi bi-mic-fill me-1"></i>Talking on TG <strong>' + (msg.tg || '?') + '</strong></span>'
        : '<span class="text-secondary"><i class="bi bi-mic-mute me-1"></i>Stopped talking</span>';
    if (msg.tg !== undefined && msg.callsign)
      return msg.tg === 0
        ? '<span class="text-secondary">Left talkgroup</span>'
        : 'Joined TG <strong class="text-primary">' + msg.tg + '</strong>';
    if (msg.msg_type)
      return '<span class="text-info">' + esc(msg.msg_type) + '</span>' +
             (msg.tg ? ' TG <strong>' + msg.tg + '</strong>' : '');
    return '<span class="text-secondary font-monospace">' +
           esc(JSON.stringify(msg).substring(0, 100)) + '</span>';
  }

  // ── WebSocket indicator ────────────────────────────────────────────────────
  function setWs(state) {
    var dot   = document.getElementById('ws-dot');
    var label = document.getElementById('ws-label');
    if (dot)   dot.className   = 'ws-dot ws-' + state;
    if (label) label.textContent =
      state === 'connected'    ? 'Live'          :
      state === 'disconnected' ? 'Disconnected'  : 'Connecting\u2026';
  }

  // ── ActionCable subscription ───────────────────────────────────────────────
  function connect() {
    setWs('connecting');
    var proto = location.protocol === 'https:' ? 'wss' : 'ws';
    var ws    = new WebSocket(proto + '://' + location.host + '/cable');

    ws.onopen = function () {
      ws.send(JSON.stringify({
        command:    'subscribe',
        identifier: JSON.stringify({ channel: 'UpdatesChannel' })
      }));
    };

    ws.onmessage = function (ev) {
      var envelope;
      try { envelope = JSON.parse(ev.data); } catch (e) { return; }
      if (envelope.type === 'welcome')              { setWs('connected'); return; }
      if (envelope.type === 'ping')                 { return; }
      if (envelope.type === 'confirm_subscription') { return; }

      var msg = envelope.message;
      if (!msg) return;

      updateCount++;
      setText('stat-updates', updateCount);

      // Full snapshot
      if (msg.nodes && typeof msg.nodes === 'object') {
        Object.keys(msg.nodes).forEach(function (cs) { updateCard(cs, msg.nodes[cs]); });
        refreshStats();
        addLog({ msg_type: 'Snapshot (' + Object.keys(msg.nodes).length + ' nodes)' });
        return;
      }

      // Single-node update
      if (msg.callsign) updateCard(msg.callsign, msg);
      refreshStats();
      addLog(msg);
    };

    ws.onclose = function () { setWs('disconnected'); setTimeout(connect, 5000); };
    ws.onerror = function () { setWs('disconnected'); };
  }

  connect();
})();
</script>
</body>
</html>
ENDVIEW

echo "Bootstrap complete."
