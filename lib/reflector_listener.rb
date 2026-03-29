require 'net/http'
require 'uri'
require 'json'

# Polls the SVXReflector/GeuReflector HTTP status API and broadcasts node-level diffs
# via ActionCable whenever tg or isTalker changes for any node.
# Also tracks trunk links, satellites, and cluster TGs (GeuReflector extensions).
class ReflectorListener
  POLL_INTERVAL = 1 # seconds
  TRUNK_THREAD_CHECK_INTERVAL = 60 # seconds — how often to check/start trunk status threads

  # Net::HTTP needs bare IPv6 addresses without the brackets that URI keeps.
  def self.http_get(url)
    uri = URI.parse(url)
    http = Net::HTTP.new(uri.host.delete('[]'), uri.port)
    http.use_ssl = uri.scheme == 'https'
    http.open_timeout = 5
    http.read_timeout = 5
    http.get(uri.request_uri)
  end

  def self.start(_host = nil, _port = nil)
    STDERR.puts "[Poller] Starting HTTP poll every #{POLL_INTERVAL}s"

    Thread.new do
      prev = {}
      prev_trunks = {}
      prev_satellites = {}
      prev_cluster_tgs = []
      last_config_fetch = 0
      start_trunk_status_threads

      loop do
        begin
          status_url = Setting.get('reflector_status_url', ENV.fetch('REFLECTOR_STATUS_URL', 'http://213.254.10.33:8181/status'))
          res  = http_get(status_url)
          status_data = JSON.parse(res.body)

          curr             = status_data.fetch('nodes', {})
          curr_trunks      = status_data.fetch('trunks', {})
          curr_satellites  = status_data.fetch('satellites', {})
          curr_cluster_tgs = status_data.fetch('cluster_tgs', [])

          # Extract config fields from /status (GeuReflector includes them inline)
          curr_config = status_data.slice('mode', 'version', 'local_prefix', 'satellite', 'cluster_tgs', 'http_port', 'listen_port').compact

          # Merge remote trunk nodes (tagged with _external)
          merge_trunk_status_nodes(curr)

          # Enrich web listener nodes with browser/location metadata stored by AudioChannel
          enrich_web_nodes(curr)

          # Enrich bridge nodes with RX metadata from Redis
          enrich_dstar_rx(curr)
          enrich_dmr_rx(curr)
          enrich_ysf_rx(curr)
          enrich_m17_rx(curr)
          enrich_zello_rx(curr)

          # ── Node diffs ────────────────────────────────────────────────────
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
            next true if node['zello_rx'] != p['zello_rx']
            # Also trigger when any RX squelch opens/closes (gives fresh siglev data)
            node_rx = node.dig('qth', 0, 'rx') || {}
            prev_rx = p.dig('qth', 0, 'rx') || {}
            node_rx.any? { |port, rx| rx['sql_open'] != prev_rx.dig(port, 'sql_open') }
          end

          removed = prev.keys - curr.keys

          # ── Trunk/satellite/cluster diffs ──────────────────────────────────
          trunks_changed     = curr_trunks != prev_trunks
          satellites_changed = curr_satellites != prev_satellites
          cluster_changed    = curr_cluster_tgs.sort != prev_cluster_tgs.sort

          anything_changed = !changed.empty? || !removed.empty? || trunks_changed || satellites_changed || cluster_changed

          if anything_changed
            payload = { nodes: curr, changed: changed.keys, removed: removed,
                        _ts: Time.now.iso8601 }
            payload[:trunks]      = curr_trunks      if trunks_changed || !changed.empty? || !removed.empty?
            payload[:satellites]  = curr_satellites   if satellites_changed || !changed.empty? || !removed.empty?
            payload[:cluster_tgs] = curr_cluster_tgs  if cluster_changed || !changed.empty? || !removed.empty?
            ActionCable.server.broadcast('updates', payload)
            parts = []
            parts << "#{changed.keys.size} nodes changed" unless changed.empty?
            parts << "#{removed.size} removed" unless removed.empty?
            parts << "trunks updated" if trunks_changed
            parts << "satellites updated" if satellites_changed
            parts << "cluster_tgs updated" if cluster_changed
            STDERR.puts "[Poller] Broadcast: #{parts.join(', ')}"
          end

          # ── Persist events ──────────────────────────────────────────────────
          log_events(changed, removed, prev)
          log_trunk_events(curr_trunks, prev_trunks) if trunks_changed
          log_satellite_events(curr_satellites, prev_satellites) if satellites_changed

          # ── Sync cluster TGs to database ────────────────────────────────────
          sync_cluster_tgs(curr_cluster_tgs) if cluster_changed

          # ── Cache in Redis ──────────────────────────────────────────────────
          cache_all(curr, curr_trunks, curr_satellites, curr_cluster_tgs, curr_config)

          # ── Manage trunk status threads periodically ────────────────────────
          now = Time.now.to_i
          if now - last_config_fetch >= TRUNK_THREAD_CHECK_INTERVAL
            start_trunk_status_threads
            last_config_fetch = now
          end

          prev = curr
          prev_trunks = curr_trunks
          prev_satellites = curr_satellites
          prev_cluster_tgs = curr_cluster_tgs
        rescue => e
          STDERR.puts "[Poller] Error: #{e.message}"
        end
        interval = (Setting.get('poll_interval') || POLL_INTERVAL).to_i.clamp(1, 10)
        sleep interval
      end
    end
  end

  def self.cache_all(nodes, trunks, satellites, cluster_tgs, config = {})
    redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
    redis.pipelined do |pipe|
      pipe.set('reflector:snapshot', nodes.to_json)
      pipe.set('reflector:trunks', trunks.to_json)
      pipe.set('reflector:satellites', satellites.to_json)
      pipe.set('reflector:cluster_tgs', cluster_tgs.to_json)
      pipe.set('reflector:config', config.to_json) if config.any?
      # Cache trunk status data for remote peer view on /trunks
      trunk_data = @trunk_status_mutex.synchronize { @trunk_status_cache.dup }
      trunk_data.each do |name, data|
        pipe.set("reflector:trunk_status:#{name}", data.to_json, ex: 60)
      end
    end
  rescue => e
    STDERR.puts "[Poller] Redis cache error: #{e.message}"
  end


  # ── Non-blocking trunk status polling ────────────────────────────────────
  # Each trunk with a STATUS_URL gets its own background thread that fetches
  # on a 5s interval and caches the result. The main poll loop reads from
  # the cache — never blocks on HTTP.
  @trunk_status_cache = {}   # { trunk_name => { nodes } }
  @trunk_status_threads = {} # { trunk_name => Thread }
  @trunk_status_mutex = Mutex.new

  def self.start_trunk_status_threads
    # Read trunk STATUS_URLs from Redis (published by the web container)
    redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
    raw = redis.get('reflector:trunk_status_urls')
    trunk_urls = raw ? JSON.parse(raw) : {}

    trunk_urls.each do |name, url|
      next if url.blank?
      next if @trunk_status_threads[name]&.alive?

      @trunk_status_threads[name] = Thread.new(name, url) do |tname, turl|
        STDERR.puts "[Poller] Trunk #{tname} status thread started (#{turl})"
        loop do
          begin
            uri = URI.parse(turl)
            http = Net::HTTP.new(uri.host.delete('[]'), uri.port)
            http.use_ssl = uri.scheme == 'https'
            http.open_timeout = 10
            http.read_timeout = 10
            res = http.get(uri.request_uri)
            if res.is_a?(Net::HTTPSuccess)
              data = JSON.parse(res.body)
              @trunk_status_mutex.synchronize { @trunk_status_cache[tname] = data }
            end
          rescue => e
            STDERR.puts "[Poller] Trunk #{tname} status fetch failed: #{e.message}"
          end
          sleep 5
        end
      end
    end

    # Clean up threads for trunks that no longer have a STATUS_URL
    trunk_names = trunk_urls.keys
    (@trunk_status_threads.keys - trunk_names).each do |old|
      @trunk_status_threads.delete(old)&.kill
      @trunk_status_mutex.synchronize { @trunk_status_cache.delete(old) }
    end
  end

  # Merge cached trunk nodes into the current snapshot (non-blocking).
  def self.merge_trunk_status_nodes(curr)
    cached = @trunk_status_mutex.synchronize { @trunk_status_cache.dup }
    cached.each do |name, data|
      (data['nodes'] || {}).each do |cs, node|
        next if node['hidden']
        next if curr.key?(cs)
        curr[cs] = node.merge('_external' => name, '_external_type' => 'trunk')
      end
    end
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
          rx_meta = node['dstar_rx'] || node['dmr_rx'] || node['ysf_rx'] || node['zello_rx']
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

  # Log trunk connection state changes and remote talker start/stop events
  def self.log_trunk_events(curr_trunks, prev_trunks)
    ActiveRecord::Base.connection_pool.with_connection do
      all_names = (curr_trunks.keys + prev_trunks.keys).uniq
      all_names.each do |name|
        curr_trunk = curr_trunks[name]
        prev_trunk = prev_trunks[name]

        # Trunk appeared or connected
        if curr_trunk && (!prev_trunk || curr_trunk['connected'] != prev_trunk['connected'])
          if curr_trunk['connected']
            NodeEvent.create!(callsign: name, event_type: NodeEvent::TRUNK_CONNECTED,
                              source: name, metadata: { host: curr_trunk['host'] }.to_json)
          else
            NodeEvent.create!(callsign: name, event_type: NodeEvent::TRUNK_DISCONNECTED,
                              source: name, metadata: { host: curr_trunk['host'] }.to_json)
          end
        end

        # Trunk disappeared
        if prev_trunk && !curr_trunk
          NodeEvent.create!(callsign: name, event_type: NodeEvent::TRUNK_DISCONNECTED,
                            source: name, metadata: { host: prev_trunk['host'] }.to_json)
        end

        # Remote talker changes
        next unless curr_trunk && prev_trunk
        curr_talkers = curr_trunk['active_talkers'] || {}
        prev_talkers = prev_trunk['active_talkers'] || {}

        # New remote talkers
        curr_talkers.each do |tg, callsign|
          unless prev_talkers[tg] == callsign
            NodeEvent.create!(callsign: callsign, event_type: NodeEvent::REMOTE_TALK_START,
                              tg: tg.to_i, source: name)
          end
        end

        # Stopped remote talkers
        prev_talkers.each do |tg, callsign|
          unless curr_talkers.key?(tg)
            NodeEvent.create!(callsign: callsign, event_type: NodeEvent::REMOTE_TALK_STOP,
                              tg: tg.to_i, source: name)
          end
        end
      end
    end
  rescue => e
    STDERR.puts "[Poller] Trunk event DB error: #{e.message}"
  end

  # Log satellite connection state changes
  def self.log_satellite_events(curr_satellites, prev_satellites)
    ActiveRecord::Base.connection_pool.with_connection do
      all_ids = (curr_satellites.keys + prev_satellites.keys).uniq
      all_ids.each do |sat_id|
        curr_sat = curr_satellites[sat_id]
        prev_sat = prev_satellites[sat_id]

        if curr_sat && !prev_sat
          NodeEvent.create!(callsign: sat_id, event_type: NodeEvent::SAT_CONNECTED)
        elsif !curr_sat && prev_sat
          NodeEvent.create!(callsign: sat_id, event_type: NodeEvent::SAT_DISCONNECTED)
        elsif curr_sat && prev_sat && curr_sat['authenticated'] != prev_sat['authenticated']
          type = curr_sat['authenticated'] ? NodeEvent::SAT_CONNECTED : NodeEvent::SAT_DISCONNECTED
          NodeEvent.create!(callsign: sat_id, event_type: type)
        end
      end
    end
  rescue => e
    STDERR.puts "[Poller] Satellite event DB error: #{e.message}"
  end

  # Ensure cluster TGs exist in the tgs table with kind='cluster'
  def self.sync_cluster_tgs(cluster_tgs)
    ActiveRecord::Base.connection_pool.with_connection do
      cluster_tgs.each do |tg_num|
        Tg.find_or_create_by(tg: tg_num) do |tg|
          tg.name = "Cluster TG #{tg_num}"
          tg.kind = 'cluster'
        end
      end
      # Update kind for any existing TGs that are now cluster
      Tg.where(tg: cluster_tgs).where.not(kind: 'cluster').update_all(kind: 'cluster')
    end
  rescue => e
    STDERR.puts "[Poller] Cluster TG sync error: #{e.message}"
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

  def self.enrich_zello_rx(nodes)
    redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
    keys = redis.keys('zello_rx:*')
    return if keys.empty?

    keys.each do |key|
      callsign = key.sub('zello_rx:', '').strip
      next unless nodes.key?(callsign)
      val = redis.get(key)
      next unless val
      data = JSON.parse(val) rescue next
      nodes[callsign]['zello_rx'] = data
    end
  rescue => e
    STDERR.puts "[Poller] Zello RX enrich error: #{e.message}"
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
