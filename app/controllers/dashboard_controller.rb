class DashboardController < ApplicationController
  layout false

  def index
    fetch_nodes
    fetch_extended
    load_sql_timeout
  end

  def map
    fetch_nodes
    fetch_extended
    fetch_external_reflectors
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
  end

  def radio
    fetch_nodes
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
      .group_by { |_, n| n['nodeClass'].to_s.presence || 'unknown' }
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
    @online_users = redis.keys("session:*").size
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
    http = Net::HTTP.new(uri.host, uri.port)
    http.use_ssl = uri.scheme == "https"
    http.open_timeout = 3
    http.read_timeout = 5
    response = http.get(uri.request_uri)
    return nil unless response.is_a?(Net::HTTPSuccess)
    data = JSON.parse(response.body)
    data["nodes"] || {}
  rescue => e
    Rails.logger.warn "[ExternalReflectors] Failed to fetch #{url}: #{e.message}"
    nil
  end
end
