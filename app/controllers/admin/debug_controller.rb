module Admin
  class DebugController < ApplicationController
    layout false
    before_action :require_admin

    def show
      load_redis_data
      respond_to do |format|
        format.html
        format.json { render json: { info: @redis_info, keys: @keys, pubsub_channels: @pubsub_channels, selected_db: @selected_db, databases: @databases } }
      end
    end

    private

    def load_redis_data
      redis_url = ENV.fetch("REDIS_URL", "redis://redis:6379/1")
      @default_db = URI.parse(redis_url).path.to_s.delete_prefix("/").presence || "0"
      @selected_db = params[:db].present? ? params[:db].to_i.to_s : @default_db

      # Connect to the selected database
      uri = URI.parse(redis_url)
      uri.path = "/#{@selected_db}"
      redis = Redis.new(url: uri.to_s)

      info = redis.info
      @redis_info = info.slice(
        "redis_version", "uptime_in_seconds", "connected_clients",
        "used_memory_human", "used_memory_peak_human",
        "total_connections_received", "total_commands_processed"
      )
      @redis_info["database"] = @selected_db

      # Collect which databases have keys for the selector
      @databases = (0..15).map { |i| { index: i, info: info["db#{i}"] } }.select { |d| d[:info] || d[:index].to_s == @selected_db }

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
