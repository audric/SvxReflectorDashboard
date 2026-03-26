redis_url = ENV.fetch("REDIS_URL", "redis://redis:6379/1")

Rails.application.config.session_store :cache_store,
  key: "_svx_session",
  expire_after: 24.hours,
  cache: ActiveSupport::Cache::RedisCacheStore.new(url: redis_url, namespace: "session")
