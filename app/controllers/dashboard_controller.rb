class DashboardController < ApplicationController
  layout false

  def index
    fetch_nodes
  end

  def map
    fetch_nodes
  end

  def tg
    fetch_nodes

    visible = @nodes.reject { |_, n| n['hidden'] }

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

    @sorted_nodes = visible.sort_by { |cs, n|
      [n['isTalker'] ? 0 : (n['tg'].to_i != 0 ? 1 : 2), cs]
    }

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

    @recent_events = NodeEvent.order(created_at: :desc).limit(30)
  end

  private

  def fetch_nodes
    @reflector_host = Setting.get('brand_name', ENV.fetch('BRAND_NAME', '213.254.10.33'))
    begin
      redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
      data  = redis.get('reflector:snapshot')
      @nodes = data ? JSON.parse(data) : {}
    rescue => e
      @nodes       = {}
      @fetch_error = e.message
    end
  end
end
