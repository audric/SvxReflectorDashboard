module Admin
  class DebugController < ApplicationController
    layout false
    before_action :require_admin

    def show
      load_redis_data
      respond_to do |format|
        format.html
        format.json { render json: { info: @redis_info, keys: @keys, pubsub_channels: @pubsub_channels } }
      end
    end

    private

    def load_redis_data
      redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))

      @redis_info = redis.info.slice(
        "redis_version", "uptime_in_seconds", "connected_clients",
        "used_memory_human", "used_memory_peak_human", "db0",
        "total_connections_received", "total_commands_processed"
      )

      @keys = redis.keys("*").sort.map do |key|
        type = redis.type(key)
        ttl = redis.ttl(key)
        size = nil
        value = case type
                when "string"
                  val = redis.get(key)
                  size = val.bytesize
                  val.force_encoding("UTF-8").scrub("?")
                when "hash"
                  h = redis.hgetall(key)
                  size = h.size
                  h.to_json
                when "set"
                  members = redis.smembers(key)
                  size = members.size
                  members.to_json
                when "list"
                  items = redis.lrange(key, 0, 50)
                  size = redis.llen(key)
                  items.to_json
                when "zset"
                  members = redis.zrange(key, 0, 50, with_scores: true)
                  size = redis.zcard(key)
                  members.to_json
                else
                  "(#{type})"
                end
        { key: key, type: type, ttl: ttl, size: size, value: value }
      end

      @pubsub_channels = redis.pubsub("channels")

      redis.close
    rescue => e
      @error = "#{e.class}: #{e.message}"
    end
  end
end
