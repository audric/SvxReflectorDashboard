redis_url = ENV.fetch("REDIS_URL", "redis://redis:6379/1")

Rails.application.config.session_store :cache_store,
  key: "_svx_session",
  expire_after: 24.hours,
  same_site: :lax,
  # force_ssl would otherwise default this to true and drop the session cookie
  # on plain-HTTP requests. ActionDispatch::SSL still adds the Secure flag to
  # every cookie on requests that arrived over HTTPS.
  secure: false,
  cache: ActiveSupport::Cache::RedisCacheStore.new(url: redis_url, namespace: "session")
