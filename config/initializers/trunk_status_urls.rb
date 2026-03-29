# Publish trunk STATUS_URLs to Redis on boot so the updater can start
# polling threads without needing access to svxreflector.conf.
Rails.application.config.after_initialize do
  next unless File.exist?(ReflectorConfig.config_path)

  config = ReflectorConfig.load
  urls = {}
  config.trunks.each do |name, cfg|
    urls[name] = cfg['STATUS_URL'] if cfg['STATUS_URL'].present?
  end
  next if urls.empty?

  redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://redis:6379/1'))
  redis.set('reflector:trunk_status_urls', urls.to_json)
rescue => e
  Rails.logger.warn "[TrunkStatusURLs] Failed to publish on boot: #{e.message}"
end
