class DashboardController < ApplicationController
  layout false

  def index
    fetch_nodes
    fetch_extended
    fetch_external_reflectors
    merge_external_nodes
    load_sql_timeout
  end

  def map
    fetch_nodes
    fetch_extended
    fetch_external_reflectors
    merge_external_nodes

    all_visible = @nodes.reject { |_, n| n['hidden'] }
    # Only show nodes with valid coordinates
    @nodes = all_visible.select do |_, n|
      pos = n.dig('qth', 0, 'pos')
      pos && pos['lat'].present? && pos['long'].present?
    end
    @off_map_nodes = all_visible.reject { |cs, _| @nodes.key?(cs) }
  end

  def tg
    fetch_nodes
    fetch_extended
    visible = @nodes.reject { |_, n| n['hidden'] }

    @tgs_db = Tg.ordered

    # Build a map of TG → list of nodes on that TG (selected or monitoring)
    @tg_nodes = {}
    visible.each do |cs, node|
      selected_tg = node['tg'].to_i
      monitored = Array(node['monitoredTGs']).map(&:to_i)
      ([selected_tg] + monitored).uniq.select { |t| t > 0 }.each do |tg|
        (@tg_nodes[tg] ||= []) << { callsign: cs, selected: selected_tg == tg, node: node }
      end
    end

    # Routing info: local prefix, trunk prefixes, cluster TGs
    config = ReflectorConfig.load
    local_prefix = config.global["LOCAL_PREFIX"].to_s.split(",").map(&:strip).reject(&:blank?)
    trunk_routes = config.trunks.map { |name, cfg|
      status_url = cfg["STATUS_URL"].presence
      parsed = status_url ? (URI.parse(status_url) rescue nil) : nil
      portal_url = parsed ? "#{parsed.scheme}://#{parsed.host}" : nil
      { name: name, prefix: cfg["REMOTE_PREFIX"].to_s.split(",").map(&:strip).reject(&:blank?), trunk_host: cfg["HOST"], portal_url: portal_url }
    }
    cluster_tgs = @cluster_tgs

    parent_host = @reflector_config.dig('satellite', 'parent_host') || @reflector_config.dig('satellite', 'host') || config.global['SATELLITE_OF']

    @route_for = ->(tg_num) {
      tg_s = tg_num.to_s
      if cluster_tgs.include?(tg_num)
        { label: "cluster", css: "bg-cyan-900/30 text-cyan-400 border-cyan-700" }
      elsif @reflector_mode == 'satellite' && parent_host.present?
        { label: parent_host, css: "bg-purple-900/30 text-purple-400 border-purple-700", url: "https://#{parent_host}" }
      elsif (trunk = trunk_routes.filter_map { |t| match = t[:prefix].select { |p| tg_s.start_with?(p) }.max_by(&:length); match ? [t, match.length] : nil }.max_by(&:last)&.first)
        { label: trunk[:trunk_host] || trunk[:name].sub(/\ATRUNK_/, ""), css: "bg-purple-900/30 text-purple-400 border-purple-700", url: trunk[:portal_url] }
      elsif local_prefix.any? { |p| tg_s.start_with?(p) }
        { label: "local", css: "bg-green-900/30 text-green-400 border-green-700" }
      else
        { label: "\u2014", css: nil }
      end
    }
  end

  def radio
    fetch_nodes
    fetch_extended
    fetch_external_reflectors
    merge_external_nodes
    bridge_classes = %w[bridge xlx dmr ysf allstar echolink web].freeze
    visible = @nodes.reject { |_, n| n['hidden'] || bridge_classes.include?(n['nodeClass'].to_s) }

    all_tg_set = []
    raw_rows = visible.sort_by { |cs, _| cs }.map do |cs, node|
      ttg = node['toneToTalkgroup'] || {}
      tg_tone = ttg.each_with_object({}) do |(tone_str, tg_num), h|
        all_tg_set << tg_num
        h[tg_num] = tone_str.to_f
      end
      [cs, node, tg_tone]
    end

    @all_tgs   = all_tg_set.uniq.sort
    @node_rows = raw_rows.select { |_, _, tg_tone| tg_tone.any? }
  end

  def stats
    fetch_nodes
    fetch_extended

    visible = @nodes.reject { |_, n| n['hidden'] }

    # ── Live counts (current snapshot) ────────────────────────────────────────
    @total   = visible.size
    @active  = visible.count { |_, n| n['tg'].to_i != 0 }
    @talking = visible.count { |_, n| n['isTalker'] }
    @idle    = @total - @active

    @by_class = visible
      .group_by { |_, n| n['nodeClass'].to_s.downcase.presence || 'unknown' }
      .sort_by   { |cls, _| cls }
      .map       { |cls, arr| [cls, arr.size] }

    @active_tgs = visible
      .select    { |_, n| n['tg'].to_i != 0 }
      .group_by  { |_, n| n['tg'].to_i }
      .transform_values { |arr| arr.map { |cs, _| cs }.sort }
      .sort_by   { |tg, _| tg }

    # ── Historical usage (filterable by period) ────────────────────────────────
    @period = params[:period].presence_in(%w[day month year]) || 'all'
    scope   = NodeEvent.by_period(@period)

    @hist_talks         = scope.talks.count
    @hist_tg_joins      = scope.tg_joins.count
    @hist_unique_nodes  = scope.talks.distinct.count(:callsign)
    @hist_unique_tgs    = scope.talks.where.not(tg: [0, nil]).distinct.count(:tg)

    @top_talkers = scope.talks
                        .group(:callsign)
                        .order('count_all DESC')
                        .limit(15)
                        .count

    @top_tgs = scope.talks
                    .where.not(tg: [0, nil])
                    .group(:tg)
                    .order('count_all DESC')
                    .limit(15)
                    .count

    # Top nodes vs bridges: split by node class from live snapshot
    bridge_callsigns = visible.select { |_, n| %w[bridge xlx dmr ysf allstar echolink].include?(n['nodeClass'].to_s) }.map(&:first).to_set
    node_callsigns = visible.reject { |cs, _| bridge_callsigns.include?(cs) }.map(&:first).to_set

    @top_nodes = scope.talks
                      .where(callsign: node_callsigns.to_a)
                      .group(:callsign)
                      .order('count_all DESC')
                      .limit(10)
                      .count

    @top_bridges = scope.talks
                        .where(callsign: bridge_callsigns.to_a)
                        .group(:callsign)
                        .order('count_all DESC')
                        .limit(10)
                        .count

    @bridge_type_counts = visible
      .select { |_, n| %w[bridge xlx dmr ysf allstar echolink].include?(n['nodeClass'].to_s) }
      .group_by { |_, n| n['nodeClass'].to_s }
      .transform_values(&:size)
      .sort_by { |_, count| -count }

    # ── Web users ─────────────────────────────────────────────────────────────
    @total_users  = User.where(approved: true).count
    redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
    all_sessions = redis.keys("session:*")
    @online_users = all_sessions.count { |k| redis.get(k).to_s.include?("user_id") }
    @anonymous_sessions = all_sessions.size - @online_users
    redis.close

    # ── Trunk traffic stats ────────────────────────────────────────────────────
    @trunk_traffic = scope.where.not(source: [nil, ''])
                          .group(:source)
                          .order('count_all DESC')
                          .count

    # ── Cluster TG usage ───────────────────────────────────────────────────────
    if @cluster_tgs.any?
      @cluster_tg_usage = scope.talks
                               .where(tg: @cluster_tgs)
                               .group(:tg)
                               .order('count_all DESC')
                               .count
    else
      @cluster_tg_usage = {}
    end
  end

  def trunks
    fetch_extended

    # Local trunk configuration from svxreflector.conf
    @local_config = ReflectorConfig.load
    @local_trunks = @local_config.trunks
    @local_prefix = @local_config.global['LOCAL_PREFIX']

    # Read remote peer status from Redis (cached by trunk status threads)
    # Falls back to direct fetch if Redis key is missing (e.g. after restart)
    @remote_configs = {}
    redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://redis:6379/1'))
    @local_trunks.each do |name, cfg|
      data = redis.get("reflector:trunk_status:#{name}")
      if data
        @remote_configs[name] = JSON.parse(data)
      elsif cfg['STATUS_URL'].present?
        begin
          uri = URI.parse(cfg['STATUS_URL'])
          http = Net::HTTP.new(uri.host.delete('[]'), uri.port)
          http.use_ssl = uri.scheme == 'https'
          http.open_timeout = 3
          http.read_timeout = 3
          res = http.get(uri.request_uri)
          if res.is_a?(Net::HTTPSuccess)
            parsed = JSON.parse(res.body)
            redis.set("reflector:trunk_status:#{name}", parsed.to_json, ex: 60)
            @remote_configs[name] = parsed
          end
        rescue => e
          Rails.logger.debug "[Trunks] Fallback fetch for #{name} failed: #{e.message}"
        end
      end
    end

    # In satellite mode, fetch parent's /status for topology prefix info
    if @reflector_mode == 'satellite'
      parent_host = @reflector_config.dig('satellite', 'parent_host') || @reflector_config.dig('satellite', 'host')
      if parent_host.present?
        cache_key = "reflector:parent_status"
        data = redis.get(cache_key)
        if data
          @parent_status = JSON.parse(data)
        else
          begin
            uri = URI.parse("https://#{parent_host}/status")
            http = Net::HTTP.new(uri.host, uri.port)
            http.use_ssl = true
            http.open_timeout = 3
            http.read_timeout = 3
            res = http.get(uri.request_uri)
            if res.is_a?(Net::HTTPSuccess)
              @parent_status = JSON.parse(res.body)
              redis.set(cache_key, @parent_status.to_json, ex: 60)
            end
          rescue => e
            Rails.logger.debug "[Trunks] Parent status fetch failed: #{e.message}"
          end
        end
      end
    end
    @parent_status ||= {}

    # Recent trunk and satellite events
    @recent_events = NodeEvent.where(event_type: [
      NodeEvent::TRUNK_CONNECTED, NodeEvent::TRUNK_DISCONNECTED,
      NodeEvent::REMOTE_TALK_START, NodeEvent::REMOTE_TALK_STOP,
      NodeEvent::SAT_CONNECTED, NodeEvent::SAT_DISCONNECTED
    ]).order(created_at: :desc).limit(50)
  end

  def events
    @recent_events = NodeEvent.order(created_at: :desc).limit(100)

    # Calculate durations for talking_stop events
    @durations = {}
    @recent_events.select { |ev| ev.event_type == 'talking_stop' }.each do |stop_ev|
      start_ev = NodeEvent.where(callsign: stop_ev.callsign, event_type: 'talking_start')
                          .where('created_at < ?', stop_ev.created_at)
                          .order(created_at: :desc)
                          .first
      if start_ev
        @durations[stop_ev.id] = (stop_ev.created_at - start_ev.created_at).round
      end
    end

  end

  private

  def load_sql_timeout
    config = ReflectorConfig.load
    val = config.global["SQL_TIMEOUT"].to_i
    @sql_timeout_ms = val > 0 ? val * 1000 : nil
  end

  def merge_external_nodes
    return unless @external_reflectors.is_a?(Hash)
    @external_reflectors.each do |ref_name, ref_data|
      (ref_data[:nodes] || {}).each do |cs, node|
        next if node['hidden']
        next if @nodes.key?(cs) # local node takes precedence
        @nodes[cs] = node.merge('_external' => ref_name, '_external_portal' => ref_data[:portal_url], '_external_type' => 'svx')
      end
    end
  end

  def fetch_nodes
    begin
      redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
      data  = redis.get('reflector:snapshot')
      @nodes = data ? JSON.parse(data) : {}
    rescue => e
      @nodes       = {}
      @fetch_error = e.message
    end
  end

  def fetch_extended
    begin
      redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
      @trunks          = JSON.parse(redis.get('reflector:trunks') || '{}')
      @satellites       = JSON.parse(redis.get('reflector:satellites') || '{}')
      @cluster_tgs      = JSON.parse(redis.get('reflector:cluster_tgs') || '[]')
      @reflector_config = JSON.parse(redis.get('reflector:config') || '{}')
      @reflector_mode   = @reflector_config['mode'] || 'reflector'
    rescue => e
      @trunks          = {}
      @satellites       = {}
      @cluster_tgs      = []
      @reflector_config = {}
      @reflector_mode   = 'reflector'
    end
  end

  # Net::HTTP needs bare IPv6 addresses without brackets;
  # URI.parse keeps them as part of the host string.
  def http_for_uri(uri)
    http = Net::HTTP.new(uri.host.delete('[]'), uri.port)
    http.use_ssl = uri.scheme == "https"
    http.open_timeout = 3
    http.read_timeout = 5
    http
  end


  def fetch_external_reflectors
    @external_reflectors = {}
    ExternalReflector.enabled.ordered.each do |ref|
      nodes = Rails.cache.fetch("external_reflector:#{ref.id}", expires_in: 90.seconds) do
        fetch_remote_nodes(ref.status_url)
      end
      @external_reflectors[ref.name] = {
        nodes: nodes || {},
        portal_url: ref.portal_url
      }
    end
  rescue => e
    Rails.logger.warn "[ExternalReflectors] #{e.message}"
    @external_reflectors ||= {}
  end

  def fetch_remote_nodes(url)
    require "net/http"
    uri = URI(url)
    response = http_for_uri(uri).get(uri.request_uri)
    return nil unless response.is_a?(Net::HTTPSuccess)
    data = JSON.parse(response.body)
    data["nodes"] || {}
  rescue => e
    Rails.logger.warn "[ExternalReflectors] Failed to fetch #{url}: #{e.message}"
    nil
  end
end
