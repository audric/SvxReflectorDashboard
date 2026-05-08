# frozen_string_literal: true

require 'mqtt'
require 'json'

# MQTT subscriber source for ReflectorListener.
# Connects to the reflector's MQTT broker and subscribes to all topics under the
# configured prefix. The retained `status` message provides a full initial snapshot;
# subsequent talker/client/trunk events are merged incrementally into @last_status
# and fed through the shared processing pipeline.
#
# Falls back to HttpSource on connection failure.
class ReflectorListener
  module MqttSource
    @last_status = nil
    @status_mutex = Mutex.new

    def self.run
      STDERR.puts "[MqttSource] Starting MQTT subscriber source"

      config = ReflectorConfig.load
      mqtt_conf = config.mqtt

      unless mqtt_conf['HOST'].present?
        STDERR.puts "[MqttSource] MQTT HOST not configured, falling back to HTTP"
        fallback!
        return
      end

      full_prefix = build_prefix(mqtt_conf)
      STDERR.puts "[MqttSource] Connecting to #{mqtt_conf['HOST']}:#{mqtt_conf['PORT'] || 1883}, prefix=#{full_prefix}"

      client = MQTT::Client.new(
        host:     mqtt_conf['HOST'],
        port:     (mqtt_conf['PORT'] || 1883).to_i,
        username: mqtt_conf['USERNAME'].presence,
        password: mqtt_conf['PASSWORD'].presence,
        ssl:      mqtt_conf['TLS_ENABLED'].to_s.downcase == 'true'
      )

      client.connect
      client.subscribe("#{full_prefix}/#")
      STDERR.puts "[MqttSource] Subscribed to #{full_prefix}/#"

      loop do
        topic, payload = client.get
        begin
          relative = topic.delete_prefix("#{full_prefix}/")
          STDERR.puts "[MqttSource] Received: #{relative} (#{payload.to_s.bytesize} bytes)"
          handle_message(relative, payload)
        rescue => e
          STDERR.puts "[MqttSource] Message handling error on #{topic}: #{e.message}"
          STDERR.puts e.backtrace.first(3).join("\n")
        end
      end
    rescue MQTT::Exception, Errno::ECONNREFUSED, Errno::ECONNRESET, Errno::EHOSTUNREACH,
           SocketError, IOError, OpenSSL::SSL::SSLError => e
      STDERR.puts "[MqttSource] Connection failed: #{e.message} — falling back to HTTP"
      fallback!
    rescue => e
      STDERR.puts "[MqttSource] Unexpected error: #{e.message} — falling back to HTTP"
      STDERR.puts e.backtrace.first(5).join("\n")
      fallback!
    end

    # ── Message routing ──────────────────────────────────────────────────

    def self.handle_message(relative_topic, payload)
      data = payload.present? ? (JSON.parse(payload) rescue {}) : {}

      case relative_topic
      when 'status'
        handle_status(data)

      when %r{\Atalker/(\d+)/start\z}
        handle_talker_start($1.to_i, data)

      when %r{\Atalker/(\d+)/stop\z}
        handle_talker_stop($1.to_i, data)

      when %r{\Aclient/([^/]+)/connected\z}
        handle_client_connected($1, data)

      when %r{\Aclient/([^/]+)/disconnected\z}
        handle_client_disconnected($1)

      when %r{\Aclient/([^/]+)/rx\z}
        handle_client_rx($1, data)

      when %r{\Aclient/([^/]+)/status\z}
        handle_client_status($1, data)

      when %r{\Atrunk/([^/]+)/(outbound|inbound)/up\z}
        handle_trunk_up($1, $2, data)

      when %r{\Atrunk/([^/]+)/(outbound|inbound)/down\z}
        handle_trunk_down($1, $2, data)

      when %r{\Apeer/([^/]+)/talker/(\d+)/start\z}
        handle_peer_talker_start($1, $2.to_i, data)

      when %r{\Apeer/([^/]+)/talker/(\d+)/stop\z}
        handle_peer_talker_stop($1, $2.to_i, data)

      when %r{\Apeer/([^/]+)/client/([^/]+)/connected\z}
        handle_peer_client_connected($1, $2, data)

      when %r{\Apeer/([^/]+)/client/([^/]+)/disconnected\z}
        handle_peer_client_disconnected($1, $2)

      when %r{\Apeer/([^/]+)/client/([^/]+)/status\z}
        handle_peer_client_status($1, $2, data)

      when %r{\Apeer/([^/]+)/client/([^/]+)/rx\z}
        handle_peer_client_rx($1, $2, data)
      end
    end

    # ── Status (retained, full snapshot) ─────────────────────────────────

    def self.handle_status(data)
      @status_mutex.synchronize { @last_status = data }
      ReflectorListener.process_snapshot(data)
    end

    # ── Talker events ────────────────────────────────────────────────────

    def self.handle_talker_start(tg, data)
      callsign = data['callsign']
      return unless callsign

      @status_mutex.synchronize do
        return unless @last_status
        node = @last_status.dig('nodes', callsign)
        if node
          node['isTalker'] = true
          node['tg'] = tg
        end
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    def self.handle_talker_stop(tg, data)
      callsign = data['callsign']
      return unless callsign

      duration_ms = data['duration_ms']

      @status_mutex.synchronize do
        return unless @last_status
        node = @last_status.dig('nodes', callsign)
        if node
          # Stash duration so the snapshot diff in log_node_events can attach
          # it to the TALKING_STOP NodeEvent. Cleared after process_snapshot.
          node['_last_duration_ms'] = duration_ms if duration_ms
          node['isTalker'] = false
        end
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot

      @status_mutex.synchronize do
        @last_status&.dig('nodes', callsign)&.delete('_last_duration_ms')
      end
    end

    # ── Client events ────────────────────────────────────────────────────

    def self.handle_client_connected(callsign, data)
      @status_mutex.synchronize do
        return unless @last_status
        @last_status['nodes'] ||= {}
        # Merge rather than replace: a reconnect on a still-tracked node
        # would otherwise wipe nodeClass/qth/sw/sysop until the next periodic
        # status, leaving repeater cards looking empty between heartbeats.
        existing = @last_status['nodes'][callsign] || {}
        @last_status['nodes'][callsign] = existing.merge(
          'tg'        => data['tg'].to_i,
          'isTalker'  => existing.fetch('isTalker', false),
          'ip'        => data['ip'].to_s,
          'connected' => Time.now.to_i.to_s
        )
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    def self.handle_client_disconnected(callsign)
      @status_mutex.synchronize do
        return unless @last_status
        @last_status['nodes']&.delete(callsign)
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    # Per-client retained rx blob (siglev/sql per receiver, debounced 500ms).
    # Skip if the node isn't in the current roster — retained `rx` from
    # previously-departed clients survives in the broker (housekeeping is
    # deferred upstream).
    def self.handle_client_rx(callsign, data)
      return if callsign.to_s.empty?
      return unless data.is_a?(Hash) && !data.empty?

      @status_mutex.synchronize do
        return unless @last_status
        node = @last_status.dig('nodes', callsign)
        return unless node
        qth_list = (node['qth'] ||= [{}])
        data.each do |port, rx_data|
          target = qth_list.find { |q| q.is_a?(Hash) && (q['rx'] || {}).key?(port) } || qth_list[0]
          target['rx'] = (target['rx'] || {}).merge(port => rx_data)
        end
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    # Per-client retained status blob (rich node info: QTH, monitored TGs,
    # rx config, etc.). Same rationale as handle_client_rx for skipping
    # unknown callsigns.
    def self.handle_client_status(callsign, data)
      return if callsign.to_s.empty?
      return unless data.is_a?(Hash) && !data.empty?

      @status_mutex.synchronize do
        return unless @last_status
        node = @last_status.dig('nodes', callsign)
        return unless node
        node.merge!(data)
        node['callsign'] = callsign
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    # ── Trunk events ─────────────────────────────────────────────────────

    def self.handle_trunk_up(peer_id, direction, data)
      @status_mutex.synchronize do
        return unless @last_status
        @last_status['trunks'] ||= {}
        trunk = @last_status['trunks'][peer_id] ||= {}
        trunk['connected'] = true
        trunk['direction'] = direction
        trunk['host'] = data['host'] if data['host']
        trunk['port'] = data['port'] if data['port']
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    def self.handle_trunk_down(peer_id, _direction, _data = nil)
      @status_mutex.synchronize do
        return unless @last_status
        if @last_status['trunks']&.key?(peer_id)
          @last_status['trunks'][peer_id]['connected'] = false
        end
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    # ── Peer talker events ───────────────────────────────────────────────
    # Published by GeuReflector when a remote talker on a trunk/satellite/twin
    # peer starts or stops. Mirrored into the matching trunk's active_talkers
    # so the existing diff/broadcast pipeline fires `remote_talk_*` events
    # without waiting for the next periodic /status snapshot.

    def self.handle_peer_talker_start(peer_id, tg, data)
      callsign = data['callsign']
      return unless callsign && peer_id

      @status_mutex.synchronize do
        return unless @last_status
        trunk = @last_status.dig('trunks', peer_id)
        if trunk
          trunk['active_talkers'] ||= {}
          trunk['active_talkers'][tg.to_s] = callsign
        end
        update_satellite_parent_node(peer_id, callsign) do |node|
          node['isTalker'] = true
          node['tg']       = tg
        end
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    def self.handle_peer_talker_stop(peer_id, tg, data = nil)
      return unless peer_id

      duration_ms = data.is_a?(Hash) ? data['duration_ms'] : nil

      @status_mutex.synchronize do
        return unless @last_status
        trunk = @last_status.dig('trunks', peer_id)
        if trunk
          if duration_ms
            # Stash duration keyed by tg so the trunk diff in log_trunk_events
            # can attach it to the matching REMOTE_TALK_STOP NodeEvent.
            trunk['_last_duration_ms_by_tg'] ||= {}
            trunk['_last_duration_ms_by_tg'][tg.to_s] = duration_ms
          end
          trunk['active_talkers']&.delete(tg.to_s)
        end

        sat = @last_status['satellite']
        if sat.is_a?(Hash) && sat['parent_id'].to_s == peer_id.to_s
          callsign = data.is_a?(Hash) ? data['callsign'] : nil
          Array(sat['parent_nodes']).each do |n|
            next unless n.is_a?(Hash)
            match = callsign ? n['callsign'] == callsign : (n['tg'].to_i == tg.to_i && n['isTalker'])
            n['isTalker'] = false if match
          end
        end
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot

      @status_mutex.synchronize do
        @last_status&.dig('trunks', peer_id, '_last_duration_ms_by_tg')&.delete(tg.to_s)
      end
    end

    # ── Peer client events ───────────────────────────────────────────────
    # In satellite mode the parent's per-client snapshots and RX state arrive
    # under peer/<parent>/client/<cs>/{status,rx}. Merge them into the matching
    # satellite.parent_nodes entry so PTT/siglev update without waiting for the
    # next periodic full status retransmit.

    def self.handle_peer_client_connected(peer_id, callsign, data)
      return if peer_id.to_s.empty? || callsign.to_s.empty?

      @status_mutex.synchronize do
        return unless @last_status
        update_satellite_parent_node(peer_id, callsign) do |node|
          node['tg'] = data['tg'].to_i if data.is_a?(Hash) && data.key?('tg')
          node['ip'] = data['ip'].to_s if data.is_a?(Hash) && data['ip']
          node['connected'] = Time.now.to_i.to_s
          node.delete('disconnected_at')
        end
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    def self.handle_peer_client_disconnected(peer_id, callsign)
      return if peer_id.to_s.empty? || callsign.to_s.empty?

      @status_mutex.synchronize do
        return unless @last_status
        sat = @last_status['satellite']
        return unless sat.is_a?(Hash) && sat['parent_id'].to_s == peer_id.to_s
        parent_nodes = sat['parent_nodes']
        parent_nodes.reject! { |n| n.is_a?(Hash) && n['callsign'] == callsign } if parent_nodes.is_a?(Array)
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    def self.handle_peer_client_status(peer_id, callsign, data)
      return if peer_id.to_s.empty? || callsign.to_s.empty?
      return unless data.is_a?(Hash)

      @status_mutex.synchronize do
        return unless @last_status
        update_satellite_parent_node(peer_id, callsign) do |node|
          node.merge!(data)
          node['callsign'] = callsign
        end
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    def self.handle_peer_client_rx(peer_id, callsign, data)
      return if peer_id.to_s.empty? || callsign.to_s.empty?
      return unless data.is_a?(Hash)

      @status_mutex.synchronize do
        return unless @last_status
        update_satellite_parent_node(peer_id, callsign) do |node|
          qth_list = (node['qth'] ||= [{}])
          data.each do |port, rx_data|
            target = qth_list.find { |q| q.is_a?(Hash) && (q['rx'] || {}).key?(port) } || qth_list[0]
            target['rx'] = (target['rx'] || {}).merge(port => rx_data)
          end
        end
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    # ── Helpers ──────────────────────────────────────────────────────────

    # Locate (or insert) the parent_nodes entry for `callsign` when `peer_id`
    # matches the satellite parent, then yield it for in-place mutation.
    # Caller must hold @status_mutex.
    def self.update_satellite_parent_node(peer_id, callsign)
      return false unless @last_status
      sat = @last_status['satellite']
      return false unless sat.is_a?(Hash) && sat['parent_id'].to_s == peer_id.to_s

      sat['parent_nodes'] ||= []
      node = sat['parent_nodes'].find { |n| n.is_a?(Hash) && n['callsign'] == callsign }
      unless node
        node = { 'callsign' => callsign }
        sat['parent_nodes'] << node
      end
      yield node
      true
    end

    def self.build_prefix(mqtt_conf)
      prefix = mqtt_conf['TOPIC_PREFIX'].to_s
      name   = (mqtt_conf['MQTT_NAME'].presence || mqtt_conf['NAME']).to_s
      if name.present?
        "#{prefix}/#{name}"
      else
        prefix
      end
    end

    def self.fallback!
      ReflectorListener.publish_active_source(:http)
      ReflectorListener::HttpSource.run
    end
  end
end
