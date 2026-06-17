class DashboardController < ApplicationController
  layout false

  after_action :close_redis

  SOURCE_FILTERS = %w[all network local trunk svx].freeze

  def index
    fetch_nodes
    fetch_extended
    @nodes_source = cookies[:nodes_source].presence_in(SOURCE_FILTERS) || 'local'
    if %w[all svx].include?(@nodes_source)
      fetch_external_reflectors
      merge_external_nodes
    else
      @external_reflectors = {}
    end
    apply_source_filter(@nodes_source)
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

    # Routing info. Mirrors GeuReflector's TrunkLink::isSharedTG /
    # Reflector::hasPrefixRoute (src/svxlink/reflector/{TrunkLink,Reflector}.cpp):
    #   \u2022 REMOTE_PREFIX  = TGs the peer owns
    #   \u2022 ROUTABLE_PREFIXES = extra prefixes the link carries; "*" is a
    #     zero-length DEFAULT ROUTE (lowest precedence, never transits)
    #   \u2022 Routing is longest-prefix-match across the WHOLE mesh: a link carries
    #     a TG only if no strictly-longer prefix matches it anywhere.
    config = ReflectorConfig.load
    split = ->(v) { v.to_s.split(",").map(&:strip).reject(&:blank?) }
    local_prefix = split.call(config.global["LOCAL_PREFIX"])
    trunk_routes = config.trunks.map { |name, cfg|
      status_url = cfg["STATUS_URL"].presence
      parsed = status_url ? (URI.parse(status_url) rescue nil) : nil
      portal_url = parsed ? "#{parsed.scheme}://#{parsed.host}" : nil
      routable = split.call(cfg["ROUTABLE_PREFIXES"])
      { name: name, label: cfg["HOST"].presence || name.sub(/\ATRUNK_/, ""),
        remote: split.call(cfg["REMOTE_PREFIX"]),
        routable: routable.reject { |p| p == "*" }, wildcard: routable.include?("*"),
        portal_url: portal_url }
    }
    cluster_tgs = @cluster_tgs

    # Mesh-wide prefix set (== Reflector::m_all_prefixes). "*" excluded.
    all_prefixes = local_prefix + trunk_routes.flat_map { |t| t[:remote] + t[:routable] }

    parent_host = @reflector_config.dig('satellite', 'parent_host') || @reflector_config.dig('satellite', 'host') || config.global['SATELLITE_OF']

    # Length of the longest entry of `list` that is a prefix of tg_s (0 if none).
    best_len = ->(tg_s, list) { list.select { |p| tg_s.start_with?(p) }.map(&:length).max || 0 }
    # Does a strictly-longer mesh prefix match tg_s? (the longest-prefix loser test)
    out_specificed = ->(tg_s, len) { all_prefixes.any? { |p| p.length > len && tg_s.start_with?(p) } }

    # Returns { badges: [...], multi_peer: bool }. A TG resolves to local and/or
    # one or more trunk peers; multi_peer flags \u22652 peers carrying the same TG
    # (overlapping prefixes \u2192 routing-loop / ambiguity risk).
    @routes_for = ->(tg_num) {
      tg_s = tg_num.to_s
      badges = []
      peer_count = 0

      if cluster_tgs.include?(tg_num)
        badges << { label: "cluster", css: "bg-cyan-900/30 text-cyan-400 border-cyan-700",
                    title: "Cluster TG \u2014 broadcast to all trunk peers" }
      elsif @reflector_mode == 'satellite' && parent_host.present?
        badges << { label: parent_host, css: "bg-purple-900/30 text-purple-400 border-purple-700",
                    url: "https://#{parent_host}", title: "Routed to parent #{parent_host}" }
      else
        # Local owns the TG when a LOCAL_PREFIX is the most-specific mesh match.
        local_len = best_len.call(tg_s, local_prefix)
        if local_len > 0 && !out_specificed.call(tg_s, local_len)
          badges << { label: "local", css: "bg-green-900/30 text-green-400 border-green-700",
                      title: "Owned locally (LOCAL_PREFIX)" }
        end

        # Each trunk that carries this TG, per TrunkLink::isSharedTG.
        trunk_routes.each do |t|
          link_len = best_len.call(tg_s, t[:remote] + t[:routable])
          via_wildcard = false
          if link_len.zero?
            next unless t[:wildcard]   # "*" default route: only when nothing else claims it
            via_wildcard = true
          end
          next if out_specificed.call(tg_s, link_len)   # a longer prefix elsewhere wins

          peer_count += 1
          owns = link_len.positive? && best_len.call(tg_s, t[:remote]) == link_len
          if via_wildcard
            badges << { label: "\u21aa #{t[:label]} *", css: "bg-purple-900/20 text-purple-300 border-purple-800 border-dashed",
                        url: t[:portal_url], title: "Default route via #{t[:label]} (ROUTABLE_PREFIXES=*)" }
          elsif owns
            badges << { label: t[:label], css: "bg-purple-900/30 text-purple-400 border-purple-700",
                        url: t[:portal_url], title: "Owned by #{t[:label]} (REMOTE_PREFIX)" }
          else
            badges << { label: "\u21aa #{t[:label]}", css: "bg-purple-900/20 text-purple-300 border-purple-800 border-dashed",
                        url: t[:portal_url], title: "Relayed over #{t[:label]} (ROUTABLE_PREFIXES)" }
          end
        end

        badges << { label: "\u2014", css: nil } if badges.empty?
      end

      { badges: badges, multi_peer: peer_count >= 2 }
    }
  end

  def radio
    fetch_nodes
    fetch_extended
    fetch_external_reflectors
    merge_external_nodes
    bridge_classes = %w[bridge xlx dmr ysf allstar echolink zello iax sip mumble web].freeze
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

    # Bridge/node split derived from the live snapshot
    bridge_callsigns = visible.select { |_, n| %w[bridge xlx dmr ysf allstar echolink zello iax sip mumble].include?(n['nodeClass'].to_s) }.map(&:first).to_set
    node_callsigns   = visible.reject { |cs, _| bridge_callsigns.include?(cs) }.map(&:first).to_set

    @bridge_type_counts = visible
      .select { |_, n| %w[bridge xlx dmr ysf allstar echolink zello iax sip mumble].include?(n['nodeClass'].to_s) }
      .group_by { |_, n| n['nodeClass'].to_s }
      .transform_values(&:size)
      .sort_by { |_, count| -count }

    # ── Historical aggregates — cached 30s per (period, cluster_tg set) ──
    # Heavy work (LAG-window airtime SQL + group/count queries) runs once per
    # cache window; the airtime LAG query is materialized exactly once and
    # the six derived aggregates are computed in Ruby.
    @period = params[:period].presence_in(%w[all day week month year]) || 'day'
    cache_key = "stats:hist:#{@period}:#{Array(@cluster_tgs).sort.join(',')}"
    hist = Rails.cache.fetch(cache_key, expires_in: 30.seconds) do
      compute_historical_stats(@period, Array(@cluster_tgs))
    end
    hist.each { |k, v| instance_variable_set("@#{k}", v) }

    # Live-data-dependent split (depends on current bridge classification, so
    # not part of the historical cache).
    scope = NodeEvent.by_period(@period)
    @top_nodes   = scope.talks.where(callsign: node_callsigns.to_a).group(:callsign).order('count_all DESC').limit(20).count
    @top_bridges = scope.talks.where(callsign: bridge_callsigns.to_a).group(:callsign).order('count_all DESC').limit(20).count
    @top_nodes_airtime   = @airtime_by_cs.slice(*@top_nodes.keys)
    @top_bridges_airtime = @airtime_by_cs.slice(*@top_bridges.keys)

    nodes_air_keys           = @airtime_by_cs.select { |k, _| node_callsigns.include?(k) }.sort_by { |_, ms| -ms }.first(20).map(&:first)
    nodes_air_counts         = scope.talks.where(callsign: nodes_air_keys).group(:callsign).count
    @top_nodes_alt           = nodes_air_keys.each_with_object({}) { |k, h| h[k] = nodes_air_counts[k] || 0 }
    @top_nodes_alt_airtime   = nodes_air_keys.each_with_object({}) { |k, h| h[k] = @airtime_by_cs[k] || 0 }

    bridges_air_keys         = @airtime_by_cs.select { |k, _| bridge_callsigns.include?(k) }.sort_by { |_, ms| -ms }.first(20).map(&:first)
    bridges_air_counts       = scope.talks.where(callsign: bridges_air_keys).group(:callsign).count
    @top_bridges_alt         = bridges_air_keys.each_with_object({}) { |k, h| h[k] = bridges_air_counts[k] || 0 }
    @top_bridges_alt_airtime = bridges_air_keys.each_with_object({}) { |k, h| h[k] = @airtime_by_cs[k] || 0 }

    # ── Web users ─ Redis SCAN (non-blocking) + 30s cache ──────────────
    users = Rails.cache.fetch("stats:users", expires_in: 30.seconds) do
      sessions = redis.scan_each(match: "session:*").to_a
      online   = sessions.count { |k| redis.get(k).to_s.include?("user_id") }
      { total_users: User.where(approved: true).count,
        online_users: online,
        anonymous_sessions: sessions.size - online }
    end
    @total_users        = users[:total_users]
    @online_users       = users[:online_users]
    @anonymous_sessions = users[:anonymous_sessions]
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
      parent_status_url = @local_config.global['SATELLITE_STATUS_URL'].presence
      parent_status_url ||= "https://#{parent_host}/status" if parent_host.present?
      if parent_status_url.present?
        cache_key = "reflector:parent_status"
        data = redis.get(cache_key)
        if data
          @parent_status = JSON.parse(data)
        else
          begin
            uri = URI.parse(parent_status_url)
            http = Net::HTTP.new(uri.host.delete('[]'), uri.port)
            http.use_ssl = uri.scheme == 'https'
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
    fetch_nodes

    # Durations for talker-stop events. Prefer the reflector-supplied
    # duration_ms (from MQTT, sub-second precision); fall back to the
    # created_at delta against the matching start for legacy rows written
    # before the column existed.
    stop_to_start = { 'talking_stop' => 'talking_start',
                      'remote_talk_stop' => 'remote_talk_start' }
    @durations = {}
    @recent_events.select { |ev| stop_to_start.key?(ev.event_type) }.each do |stop_ev|
      if stop_ev.duration_ms
        @durations[stop_ev.id] = (stop_ev.duration_ms / 1000.0).round
        next
      end
      start_ev = NodeEvent.where(callsign: stop_ev.callsign, event_type: stop_to_start[stop_ev.event_type])
                          .where('created_at < ?', stop_ev.created_at)
                          .order(created_at: :desc)
                          .first
      if start_ev
        @durations[stop_ev.id] = (stop_ev.created_at - start_ev.created_at).round
      end
    end
  end

  private

  def apply_source_filter(source)
    case source
    when 'local'
      @nodes = @nodes.reject { |_, n| n['_external_type'] }
    when 'trunk'
      @nodes = @nodes.select { |_, n| n['_external_type'] == 'trunk' }
    when 'svx'
      @nodes = @nodes.select { |_, n| n['_external_type'] == 'svx' }
    when 'network'
      @nodes = @nodes.reject { |_, n| n['_external_type'] == 'svx' }
    end
  end

  def load_sql_timeout
    config = ReflectorConfig.load
    val = config.global["SQL_TIMEOUT"].to_i
    @sql_timeout_ms = val > 0 ? val * 1000 : nil
  end

  def merge_external_nodes
    return unless @external_reflectors.is_a?(Hash)
    @external_reflectors.each do |ref_name, ref_data|
      (ref_data[:nodes] || {}).each do |cs, node|
        next unless node.is_a?(Hash)
        next if node['hidden']
        next if @nodes.key?(cs) # local node takes precedence
        @nodes[cs] = node.merge('_external' => ref_name, '_external_portal' => ref_data[:portal_url], '_external_type' => 'svx')
      end
    end
  end

  def fetch_nodes
    begin
      data  = redis.get('reflector:snapshot')
      @nodes = data ? JSON.parse(data) : {}
    rescue => e
      @nodes       = {}
      @fetch_error = e.message
    end
  end

  def fetch_extended
    begin
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

  # One Redis client per request, lazy. Closed in close_redis (after_action).
  # Replaces per-call Redis.new which leaked T_DATA + malloc fragmentation.
  def redis
    @redis ||= Redis.new(url: ENV.fetch('REDIS_URL', 'redis://redis:6379/1'))
  end

  def close_redis
    @redis&.close
    @redis = nil
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
    nodes = data["nodes"]
    return {} unless nodes.is_a?(Hash)
    # Drop non-Hash node entries and sanitize qth
    nodes.reject! { |_, v| !v.is_a?(Hash) }
    nodes.each_value do |node|
      if node['qth'].is_a?(Array)
        node['qth'].select! { |q| q.is_a?(Hash) }
        node['qth'].each do |q|
          %w[rx tx].each { |d| q[d].is_a?(Hash) ? q[d].reject! { |_, v| !v.is_a?(Hash) } : q.delete(d) }
        end
      else
        node.delete('qth')
      end
    end
    nodes
  rescue => e
    Rails.logger.warn "[ExternalReflectors] Failed to fetch #{url}: #{e.message}"
    nil
  end

  # Heavy historical aggregates used by #stats. Cached upstream — runs ~6
  # SQL queries (was 16; the airtime materialization collapsed 6→1) plus a
  # bunch of Ruby grouping. Returns a Hash of @ivar-friendly keys.
  def compute_historical_stats(period, cluster_tgs)
    scope = NodeEvent.by_period(period)

    # Single LAG()-window materialization for airtime. Six downstream
    # aggregates derive from this in Ruby.
    records = NodeEvent.airtime_records(period: period)

    airtime_by_cs  = records.group_by { |r| r['callsign'] }
                            .transform_values { |arr| arr.sum { |r| r['effective_ms'].to_i } }
    airtime_by_tg  = records.reject { |r| r['tg'].to_i == 0 }
                            .group_by { |r| r['tg'].to_i }
                            .transform_values { |arr| arr.sum { |r| r['effective_ms'].to_i } }
    airtime_by_src = records.reject { |r| r['source'].to_s.empty? }
                            .group_by { |r| r['source'] }
                            .transform_values { |arr| arr.sum { |r| r['effective_ms'].to_i } }

    total_ms      = airtime_by_cs.values.sum
    avg_ms        = records.empty? ? nil : (total_ms / records.size)
    longest       = records.max_by { |r| r['effective_ms'].to_i }
    hist_longest  = longest && { callsign: longest['callsign'], ms: longest['effective_ms'].to_i }

    top_talkers = scope.talks.group(:callsign).order('count_all DESC').limit(20).count
    top_tgs     = scope.talks.where.not(tg: [0, nil]).group(:tg).order('count_all DESC').limit(20).count

    talkers_air_keys        = airtime_by_cs.sort_by { |_, ms| -ms }.first(20).map(&:first)
    talkers_air_counts      = scope.talks.where(callsign: talkers_air_keys).group(:callsign).count
    top_talkers_alt         = talkers_air_keys.each_with_object({}) { |k, h| h[k] = talkers_air_counts[k] || 0 }
    top_talkers_alt_airtime = talkers_air_keys.each_with_object({}) { |k, h| h[k] = airtime_by_cs[k] || 0 }

    tgs_air_keys        = airtime_by_tg.sort_by { |_, ms| -ms }.first(20).map(&:first)
    tgs_air_counts      = scope.talks.where.not(tg: [0, nil]).where(tg: tgs_air_keys).group(:tg).count
    top_tgs_alt         = tgs_air_keys.each_with_object({}) { |k, h| h[k] = tgs_air_counts[k] || 0 }
    top_tgs_alt_airtime = tgs_air_keys.each_with_object({}) { |k, h| h[k] = airtime_by_tg[k] || 0 }

    trunk_traffic    = scope.where.not(source: [nil, '']).group(:source).order('count_all DESC').count
    cluster_tg_usage = cluster_tgs.any? ? scope.talks.where(tg: cluster_tgs).group(:tg).order('count_all DESC').count : {}

    {
      hist_talks:               scope.talks.count,
      hist_tg_joins:            scope.tg_joins.count,
      hist_unique_nodes:        scope.talks.distinct.count(:callsign),
      hist_unique_tgs:          scope.talks.where.not(tg: [0, nil]).distinct.count(:tg),
      top_talkers:              top_talkers,
      top_tgs:                  top_tgs,
      hist_airtime_ms:          total_ms,
      hist_avg_tx_ms:           avg_ms,
      hist_longest:             hist_longest,
      top_talkers_airtime:      airtime_by_cs.slice(*top_talkers.keys),
      top_tgs_airtime:          airtime_by_tg.slice(*top_tgs.keys),
      top_talkers_alt:          top_talkers_alt,
      top_talkers_alt_airtime:  top_talkers_alt_airtime,
      top_tgs_alt:              top_tgs_alt,
      top_tgs_alt_airtime:      top_tgs_alt_airtime,
      trunk_traffic:            trunk_traffic,
      trunk_traffic_airtime:    airtime_by_src.slice(*trunk_traffic.keys),
      cluster_tg_usage:         cluster_tg_usage,
      cluster_tg_usage_airtime: airtime_by_tg.slice(*cluster_tg_usage.keys),
      airtime_by_cs:            airtime_by_cs,
    }
  end
end
