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

      when %r{\Atrunk/([^/]+)/(outbound|inbound)/up\z}
        handle_trunk_up($1, $2, data)

      when %r{\Atrunk/([^/]+)/(outbound|inbound)/down\z}
        handle_trunk_down($1, $2, data)
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

      @status_mutex.synchronize do
        return unless @last_status
        node = @last_status.dig('nodes', callsign)
        if node
          node['isTalker'] = false
        end
      end

      snapshot = @status_mutex.synchronize { @last_status&.deep_dup }
      ReflectorListener.process_snapshot(snapshot) if snapshot
    end

    # ── Client events ────────────────────────────────────────────────────

    def self.handle_client_connected(callsign, data)
      @status_mutex.synchronize do
        return unless @last_status
        @last_status['nodes'] ||= {}
        @last_status['nodes'][callsign] = {
          'tg'        => data['tg'].to_i,
          'isTalker'  => false,
          'ip'        => data['ip'].to_s,
          'connected' => Time.now.to_i.to_s
        }
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

    # ── Helpers ──────────────────────────────────────────────────────────

    def self.build_prefix(mqtt_conf)
      prefix = mqtt_conf['TOPIC_PREFIX'].to_s
      name   = mqtt_conf['NAME'].to_s
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
