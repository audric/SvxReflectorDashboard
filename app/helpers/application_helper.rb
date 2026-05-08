module ApplicationHelper
  def format_duration(ms)
    return '—' if ms.nil? || ms.to_i <= 0
    s = (ms.to_f / 1000.0).round
    return "#{s}s"                 if s < 60
    return "#{s / 60}m #{s % 60}s" if s < 3600
    h, rem = s.divmod(3600)
    "#{h}h #{rem / 60}m"
  end
end
