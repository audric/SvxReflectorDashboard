require 'net/http'
require 'uri'
require 'json'

# Polls the SVXReflector HTTP status API and broadcasts node-level diffs
# via ActionCable whenever tg or isTalker changes for any node.
class ReflectorListener
  POLL_INTERVAL = 4 # seconds

  def self.start(_host = nil, _port = nil)
    status_url = ENV.fetch('REFLECTOR_STATUS_URL', 'http://213.254.10.33:8181/status')
    STDERR.puts "[Poller] Starting HTTP poll → #{status_url} every #{POLL_INTERVAL}s"

    Thread.new do
      prev = {}
      loop do
        begin
          res  = Net::HTTP.get_response(URI.parse(status_url))
          curr = JSON.parse(res.body).fetch('nodes', {})

          changed = curr.select do |cs, node|
            p = prev[cs]
            next true if p.nil?
            next true if node['tg'] != p['tg'] || node['isTalker'] != p['isTalker']
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

          prev = curr
        rescue => e
          STDERR.puts "[Poller] Error: #{e.message}"
        end
        sleep POLL_INTERVAL
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
          NodeEvent.create!(attrs.merge(event_type: type))
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
end
