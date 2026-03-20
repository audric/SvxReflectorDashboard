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
end
