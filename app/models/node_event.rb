class NodeEvent < ApplicationRecord
  TALKING_START  = 'talking_start'
  TALKING_STOP   = 'talking_stop'
  TG_JOIN        = 'tg_join'
  TG_LEAVE       = 'tg_leave'
  CONNECTED      = 'connected'
  DISCONNECTED   = 'disconnected'

  validates :callsign,   presence: true
  validates :event_type, presence: true,
                         inclusion: { in: [TALKING_START, TALKING_STOP, TG_JOIN, TG_LEAVE,
                                           CONNECTED, DISCONNECTED] }

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
end
