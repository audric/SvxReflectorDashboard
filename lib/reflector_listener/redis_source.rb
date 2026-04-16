# frozen_string_literal: true

# Redis pub/sub source for ReflectorListener.
# Subscribes to the reflector's Redis `live.changed` channel and reconstructs
# snapshots from `live:*` keys on each tick, merging with a periodic HTTP fetch
# for fields that live keys don't carry (qth, nodeLocation, sw, swVer, etc.).
#
# Requires TWO Redis connections: one blocks on SUBSCRIBE, the other reads keys.
# Falls back to HttpSource on subscribe failure.
class ReflectorListener
  module RedisSource
    HTTP_MERGE_INTERVAL = 10 # seconds between full /status fetches

    def self.run
      STDERR.puts "[RedisSource] Starting Redis pub/sub source"

      config = ReflectorConfig.load
      unless config.redis_mode?
        STDERR.puts "[RedisSource] Redis not configured, falling back to HTTP"
        fallback!
        return
      end

      channel = config.redis_key("live.changed")

      # Reader connection — used inside the message callback to SCAN keys
      @reader = config.reflector_redis
      @config = config
      @last_http_fetch = Time.at(0)
      @http_snapshot = {}
      @http_mutex = Mutex.new

      # Subscriber connection — will block
      subscriber = config.reflector_redis

      STDERR.puts "[RedisSource] Subscribing to #{channel}"

      subscriber.subscribe(channel) do |on|
        on.message do |_ch, _msg|
          begin
            snapshot = build_snapshot
            ReflectorListener.process_snapshot(snapshot)
          rescue => e
            STDERR.puts "[RedisSource] Snapshot error: #{e.message}"
            STDERR.puts e.backtrace.first(5).join("\n")
          end
        end
      end
    rescue Redis::BaseConnectionError, Redis::CannotConnectError, Errno::ECONNREFUSED, Errno::ECONNRESET => e
      STDERR.puts "[RedisSource] Subscribe failed: #{e.message} — falling back to HTTP"
      fallback!
    rescue => e
      STDERR.puts "[RedisSource] Unexpected error: #{e.message} — falling back to HTTP"
      STDERR.puts e.backtrace.first(5).join("\n")
      fallback!
    end

    # ── Snapshot assembly ────────────────────────────────────────────────

    def self.build_snapshot
      nodes  = scan_clients
      trunks = scan_trunks

      # Mark talkers
      scan_talkers(nodes)

      # Periodically merge full HTTP /status for fields live keys don't carry
      merge_http_status(nodes)

      snapshot = { 'nodes' => nodes, 'trunks' => trunks }

      # Pull satellites, cluster_tgs, and config fields from the HTTP snapshot
      @http_mutex.synchronize do
        snapshot['satellites']  = @http_snapshot.fetch('satellites', {})
        snapshot['cluster_tgs'] = @http_snapshot.fetch('cluster_tgs', [])
        # Config fields (mode, version, local_prefix, satellite, etc.)
        %w[mode version local_prefix satellite twin cluster_tgs http_port listen_port].each do |key|
          snapshot[key] = @http_snapshot[key] if @http_snapshot.key?(key)
        end
      end

      snapshot
    end

    # Scan live:client:* keys → nodes hash
    def self.scan_clients
      nodes = {}
      pattern = @config.redis_key("live:client:*")
      prefix = @config.redis_key("live:client:")

      @reader.scan_each(match: pattern) do |key|
        callsign = key.delete_prefix(prefix)
        data = @reader.hgetall(key)
        next if data.empty?

        nodes[callsign] = {
          'tg'        => data['tg'].to_i,
          'isTalker'  => false,
          'connected' => data['connected_at'] || Time.now.to_i.to_s,
          'ip'        => data['ip'].to_s,
          'codecs'    => data['codecs'].to_s
        }
      end

      nodes
    end

    # Scan live:talker:* keys → mark nodes as isTalker
    def self.scan_talkers(nodes)
      pattern = @config.redis_key("live:talker:*")
      prefix = @config.redis_key("live:talker:")

      @reader.scan_each(match: pattern) do |key|
        _tg = key.delete_prefix(prefix)
        data = @reader.hgetall(key)
        next if data.empty?

        callsign = data['callsign']
        if callsign && nodes.key?(callsign)
          nodes[callsign]['isTalker'] = true
        end
      end
    end

    # Scan live:trunk:* keys → trunks hash
    def self.scan_trunks
      trunks = {}
      pattern = @config.redis_key("live:trunk:*")
      prefix = @config.redis_key("live:trunk:")

      @reader.scan_each(match: pattern) do |key|
        section = key.delete_prefix(prefix)
        data = @reader.hgetall(key)
        next if data.empty?

        trunks[section] = {
          'connected' => data['state'] == 'up',
          'peer_id'   => data['peer_id'].to_s,
          'last_hb'   => data['last_hb'].to_s
        }
      end

      trunks
    end

    # ── HTTP merge ───────────────────────────────────────────────────────

    def self.merge_http_status(nodes)
      now = Time.now
      return if (now - @last_http_fetch) < HTTP_MERGE_INTERVAL

      @last_http_fetch = now

      Thread.new do
        begin
          status_url = Setting.get('reflector_status_url',
                        ENV.fetch('REFLECTOR_STATUS_URL', 'http://213.254.10.33:8181/status'))
          res = ReflectorListener.http_get(status_url)
          data = JSON.parse(res.body)
          @http_mutex.synchronize { @http_snapshot = data }
        rescue => e
          STDERR.puts "[RedisSource] HTTP merge fetch error: #{e.message}"
        end
      end

      # Merge existing HTTP node data (from previous fetch) into live nodes.
      # Live key values (tg, isTalker) take precedence.
      @http_mutex.synchronize do
        http_nodes = @http_snapshot.fetch('nodes', {})
        nodes.each do |callsign, live_node|
          http_node = http_nodes[callsign]
          next unless http_node

          # Merge HTTP fields that live keys don't carry
          %w[qth nodeLocation sw swVer nodeClass].each do |field|
            live_node[field] = http_node[field] if http_node.key?(field) && !live_node.key?(field)
          end
        end
      end
    end

    # ── Fallback ─────────────────────────────────────────────────────────

    def self.fallback!
      ReflectorListener.publish_active_source(:http)
      ReflectorListener::HttpSource.run
    end
  end
end
