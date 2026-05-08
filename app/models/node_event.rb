class NodeEvent < ApplicationRecord
  TALKING_START      = 'talking_start'
  TALKING_STOP       = 'talking_stop'
  TG_JOIN            = 'tg_join'
  TG_LEAVE           = 'tg_leave'
  CONNECTED          = 'connected'
  DISCONNECTED       = 'disconnected'
  TRUNK_CONNECTED    = 'trunk_connected'
  TRUNK_DISCONNECTED = 'trunk_disconnected'
  REMOTE_TALK_START  = 'remote_talk_start'
  REMOTE_TALK_STOP   = 'remote_talk_stop'
  SAT_CONNECTED      = 'satellite_connected'
  SAT_DISCONNECTED   = 'satellite_disconnected'

  ALL_TYPES = [
    TALKING_START, TALKING_STOP, TG_JOIN, TG_LEAVE, CONNECTED, DISCONNECTED,
    TRUNK_CONNECTED, TRUNK_DISCONNECTED, REMOTE_TALK_START, REMOTE_TALK_STOP,
    SAT_CONNECTED, SAT_DISCONNECTED
  ].freeze

  validates :callsign,   presence: true
  validates :event_type, presence: true, inclusion: { in: ALL_TYPES }

  scope :since, ->(time) { where('created_at >= ?', time) if time }
  scope :talks,     -> { where(event_type: TALKING_START) }
  scope :tg_joins,  -> { where(event_type: TG_JOIN) }
  scope :by_period, ->(period) {
    case period
    when 'day'   then where('created_at >= ?', 1.day.ago)
    when 'month' then where('created_at >= ?', 1.month.ago)
    when 'year'  then where('created_at >= ?', 1.year.ago)
    else all
    end
  }

  scope :trunk_events,    -> { where(event_type: [TRUNK_CONNECTED, TRUNK_DISCONNECTED]) }
  scope :remote_talks,    -> { where(event_type: REMOTE_TALK_START) }
  scope :satellite_events, -> { where(event_type: [SAT_CONNECTED, SAT_DISCONNECTED]) }

  # Backfilled durations longer than this are treated as orphan starts
  # (process restart between start and stop) and dropped from totals.
  # Real duration_ms from MQTT is never capped.
  BACKFILL_CAP_SECONDS = 600

  # Returns total airtime in ms for the period, or 0.
  def self.airtime_total(period: 'all')
    sql = "SELECT COALESCE(SUM(effective_ms), 0) AS total FROM (#{effective_ms_sql(period)}) t"
    connection.select_value(sql).to_i
  end

  # Returns average transmission length in ms for the period, or nil.
  def self.airtime_avg(period: 'all')
    sql = "SELECT AVG(effective_ms) AS avg_ms FROM (#{effective_ms_sql(period)}) t"
    val = connection.select_value(sql)
    val ? val.to_i : nil
  end

  # Returns { callsign:, ms: } for the longest single transmission, or nil.
  def self.longest_tx(period: 'all')
    sql = <<~SQL
      SELECT callsign, effective_ms FROM (#{effective_ms_sql(period)}) t
      ORDER BY effective_ms DESC LIMIT 1
    SQL
    row = connection.select_one(sql)
    return nil unless row && row['effective_ms']
    { callsign: row['callsign'], ms: row['effective_ms'].to_i }
  end

  # Returns { key => total_ms } grouped by the given dimension.
  # Dimension ∈ [:callsign, :tg, :source].
  def self.airtime_by(dimension, period: 'all')
    column, extra_where =
      case dimension
      when :callsign then ['callsign', nil]
      when :tg       then ['tg',       'tg IS NOT NULL AND tg != 0']
      when :source   then ['source',   "source IS NOT NULL AND source != ''"]
      else raise ArgumentError, "unknown dimension #{dimension.inspect}"
      end

    sql = <<~SQL
      SELECT #{column} AS key, SUM(effective_ms) AS total_ms
      FROM (#{effective_ms_sql(period)}) t
      WHERE #{extra_where || '1=1'}
      GROUP BY #{column}
    SQL
    connection.select_all(sql).each_with_object({}) do |row, h|
      h[row['key']] = row['total_ms'].to_i if row['total_ms']
    end
  end

  # Builds the inner SQL that yields one row per `talking_stop` event with
  # an `effective_ms` column (real duration_ms when present, otherwise
  # backfilled from the prior matching `talking_start` per callsign,
  # capped at BACKFILL_CAP_SECONDS). Rows with no usable duration are
  # filtered out so they don't pollute averages or longest-tx queries.
  def self.effective_ms_sql(period)
    since_clause =
      case period
      when 'day'   then "AND created_at >= datetime('now','-1 day')"
      when 'month' then "AND created_at >= datetime('now','-1 month')"
      when 'year'  then "AND created_at >= datetime('now','-1 year')"
      else ''
      end

    cap = BACKFILL_CAP_SECONDS

    <<~SQL
      SELECT callsign, tg, source,
             COALESCE(
               duration_ms,
               CASE
                 WHEN prev_type = 'talking_start'
                  AND (julianday(created_at) - julianday(prev_at)) * 86400 <= #{cap}
                 THEN CAST((julianday(created_at) - julianday(prev_at)) * 86400000 AS INTEGER)
               END
             ) AS effective_ms
        FROM (
          SELECT callsign, tg, source, event_type, created_at, duration_ms,
                 LAG(created_at)  OVER (PARTITION BY callsign ORDER BY created_at) AS prev_at,
                 LAG(event_type) OVER (PARTITION BY callsign ORDER BY created_at) AS prev_type
            FROM node_events
           WHERE event_type IN ('talking_start','talking_stop')
                 #{since_clause}
        ) paired
       WHERE event_type = 'talking_stop'
         AND COALESCE(
               duration_ms,
               CASE
                 WHEN prev_type = 'talking_start'
                  AND (julianday(created_at) - julianday(prev_at)) * 86400 <= #{cap}
                 THEN 1
               END
             ) IS NOT NULL
    SQL
  end
  private_class_method :effective_ms_sql
end
