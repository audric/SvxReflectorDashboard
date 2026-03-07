Rails.application.config.after_initialize do
  Bridge.purge_old_archives
rescue => e
  Rails.logger.warn("[Bridge] Archive purge on boot failed: #{e.message}")
end
