require 'net/http'
require 'uri'
require 'json'

# Polls the SVXReflector HTTP status API and broadcasts node-level diffs
# via ActionCable whenever tg or isTalker changes for any node.
class ReflectorListener
  POLL_INTERVAL = 4 # seconds

  def self.start(_host = nil, _port = nil)
    STDERR.puts "[Poller] Starting HTTP poll every #{POLL_INTERVAL}s"

    Thread.new do
      prev = {}
      loop do
        begin
          status_url = Setting.get('reflector_status_url', ENV.fetch('REFLECTOR_STATUS_URL', 'http://213.254.10.33:8181/status'))
          res  = Net::HTTP.get_response(URI.parse(status_url))
          curr = JSON.parse(res.body).fetch('nodes', {})

          # Enrich web listener nodes with browser/location metadata stored by AudioChannel
          enrich_web_nodes(curr)

          # Enrich XLX bridge nodes with D-STAR RX metadata
          enrich_dstar_rx(curr)

          # Enrich DMR bridge nodes with DMR RX metadata
          enrich_dmr_rx(curr)

          # Enrich YSF bridge nodes with YSF RX metadata
          enrich_ysf_rx(curr)

          # Enrich M17 bridge nodes with M17 RX metadata
          enrich_m17_rx(curr)

          changed = curr.select do |cs, node|
            p = prev[cs]
            next true if p.nil?
            next true if node['tg'] != p['tg'] || node['isTalker'] != p['isTalker']
            next true if node['sw'] != p['sw'] || node['swVer'] != p['swVer']
            next true if node['nodeLocation'] != p['nodeLocation']
            next true if node['dstar_rx'] != p['dstar_rx']
            next true if node['dmr_rx'] != p['dmr_rx']
            next true if node['ysf_rx'] != p['ysf_rx']
            next true if node['m17_rx'] != p['m17_rx']
            # Also trigger when any RX squelch opens/closes (gives fresh siglev data)
            node_rx = node.dig('qth', 0, 'rx') || {}
            prev_rx = p.dig('qth', 0, 'rx') || {}
            node_rx.any? { |port, rx| rx['sql_open'] != prev_rx.dig(port, 'sql_open') }
          end

          removed = prev.keys - curr.keys

          unless changed.empty? && removed.empty?
            payload = { nodes: curr, changed: changed.keys, removed: removed,
                        _ts: Time.now.iso8601 }
            ActionCable.server.broadcast('updates', payload)
            STDERR.puts "[Poller] Broadcast: #{changed.keys} changed, #{removed} removed"
          end

          # ── Persist events ──────────────────────────────────────────────────
          log_events(changed, removed, prev)

          # Cache latest snapshot in Redis so web requests never block on HTTP
          cache_snapshot(curr)

          prev = curr
        rescue => e
          STDERR.puts "[Poller] Error: #{e.message}"
        end
        interval = (Setting.get('poll_interval') || POLL_INTERVAL).to_i.clamp(1, 10)
        sleep interval
      end
    end
  end

  def self.cache_snapshot(nodes)
    redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
    redis.set('reflector:snapshot', nodes.to_json)
  rescue => e
    STDERR.puts "[Poller] Redis cache error: #{e.message}"
  end

  def self.log_events(changed, removed, prev)
    ActiveRecord::Base.connection_pool.with_connection do
      changed.each do |cs, node|
        prev_node = prev[cs]
        attrs = { callsign: cs, tg: node['tg'].to_i,
                  node_class: node['nodeClass'], node_location: node['nodeLocation'] }

        # New node appeared
        if prev_node.nil?
          NodeEvent.create!(attrs.merge(event_type: NodeEvent::CONNECTED))
          next
        end

        # isTalker transition
        if node['isTalker'] != prev_node['isTalker']
          type = node['isTalker'] ? NodeEvent::TALKING_START : NodeEvent::TALKING_STOP
          rx_meta = node['dstar_rx'] || node['dmr_rx'] || node['ysf_rx']
          meta = rx_meta ? rx_meta.to_json : nil
          NodeEvent.create!(attrs.merge(event_type: type, metadata: meta))
        end

        # TG changed (not already captured by talker transition)
        if node['tg'] != prev_node['tg'] && node['isTalker'] == prev_node['isTalker']
          type = node['tg'].to_i != 0 ? NodeEvent::TG_JOIN : NodeEvent::TG_LEAVE
          NodeEvent.create!(attrs.merge(event_type: type))
        end
      end

      removed.each do |cs|
        prev_node = prev[cs] || {}
        NodeEvent.create!(
          callsign: cs, event_type: NodeEvent::DISCONNECTED,
          node_class: prev_node['nodeClass'], node_location: prev_node['nodeLocation']
        )
      end
    end
  rescue => e
    STDERR.puts "[Poller] DB error: #{e.message}"
  end

  def self.enrich_dstar_rx(nodes)
    redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
    keys = redis.keys('dstar_rx:*')
    return if keys.empty?

    keys.each do |key|
      callsign = key.sub('dstar_rx:', '').strip
      next unless nodes.key?(callsign)
      val = redis.get(key)
      next unless val
      data = JSON.parse(val) rescue next
      nodes[callsign]['dstar_rx'] = data
    end
  rescue => e
    STDERR.puts "[Poller] D-STAR RX enrich error: #{e.message}"
  end

  def self.enrich_dmr_rx(nodes)
    redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
    keys = redis.keys('dmr_rx:*')
    return if keys.empty?

    keys.each do |key|
      callsign = key.sub('dmr_rx:', '').strip
      next unless nodes.key?(callsign)
      val = redis.get(key)
      next unless val
      data = JSON.parse(val) rescue next
      nodes[callsign]['dmr_rx'] = data
    end
  rescue => e
    STDERR.puts "[Poller] DMR RX enrich error: #{e.message}"
  end

  def self.enrich_ysf_rx(nodes)
    redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
    keys = redis.keys('ysf_rx:*')
    return if keys.empty?

    keys.each do |key|
      callsign = key.sub('ysf_rx:', '').strip
      next unless nodes.key?(callsign)
      val = redis.get(key)
      next unless val
      data = JSON.parse(val) rescue next
      nodes[callsign]['ysf_rx'] = data
    end
  rescue => e
    STDERR.puts "[Poller] YSF RX enrich error: #{e.message}"
  end

  def self.enrich_m17_rx(nodes)
    redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
    keys = redis.keys('m17_rx:*')
    return if keys.empty?

    keys.each do |key|
      callsign = key.sub('m17_rx:', '').strip
      next unless nodes.key?(callsign)
      val = redis.get(key)
      next unless val
      data = JSON.parse(val) rescue next
      nodes[callsign]['m17_rx'] = data
    end
  rescue => e
    STDERR.puts "[Poller] M17 RX enrich error: #{e.message}"
  end

  def self.enrich_web_nodes(nodes)
    redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
    web_info = redis.hgetall('web_node_info')
    web_info.each do |callsign, json_str|
      next unless nodes.key?(callsign)
      meta = JSON.parse(json_str) rescue next
      meta.each { |k, v| nodes[callsign][k] = v if v.present? }

      # Build qth structure from nodeLocation so the map can place a marker
      loc = nodes[callsign]['nodeLocation'].to_s
      if nodes[callsign]['qth'].nil? && loc.include?(',')
        lat, lon = loc.split(',', 2).map { |s| s.strip.to_f }
        if lat != 0.0 || lon != 0.0
          nodes[callsign]['qth'] = [{ 'pos' => { 'lat' => lat, 'long' => lon } }]
        end
      end
    end
  rescue => e
    STDERR.puts "[Poller] Web node enrich error: #{e.message}"
  end
end
