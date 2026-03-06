class AudioChannel < ApplicationCable::Channel
  def subscribed
    tg = params[:tg].to_i
    if tg > 0
      stream_from "audio:tg:#{tg}"
      callsign = params[:callsign].to_s.strip.upcase
      stream_from "audio:user:#{callsign}"
      auth_key = params[:auth_key].to_s
      @web_callsign = callsign
      @web_tg = tg
      redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
      ref_key = "web_node_refs:#{callsign}"
      ref = redis.incr(ref_key)
      redis.expire(ref_key, 120) # auto-expire stale refs after 2 minutes
      if ref == 1
        # First browser session for this callsign — open reflector connection
        redis.publish("audio:commands", {
          action: "connect", tg: tg, callsign: callsign, auth_key: auth_key,
          sw: params[:sw].to_s, sw_ver: params[:sw_ver].to_s,
          node_class: params[:node_class].to_s, node_location: params[:node_location].to_s,
          sysop: params[:sysop].to_s
        }.to_json)
      end
      update_web_node_info(redis, callsign)
      @ref_key = ref_key
      redis.close
    else
      reject
    end
  end

  def unsubscribed
    return if @web_callsign.blank?

    redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
    ref = redis.decr("web_node_refs:#{@web_callsign}")
    if ref <= 0
      # Last browser session — tear down the reflector connection
      redis.del("web_node_refs:#{@web_callsign}")
      redis.publish("audio:commands", { action: "disconnect", callsign: @web_callsign }.to_json)
      redis.hdel("web_node_info", @web_callsign)
    end
  ensure
    redis&.close
  end

  def select_tg(data)
    tg = data["tg"].to_i
    return unless tg > 0

    callsign = data["callsign"].to_s.strip.upcase
    redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
    redis.publish("audio:commands", { action: "select_tg", tg: tg, callsign: callsign }.to_json)
  ensure
    redis&.close
  end

  def ptt_start(data)
    tg = data["tg"].to_i
    return unless tg > 0

    callsign = data["callsign"].to_s.strip.upcase
    redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
    redis.publish("audio:tx", { action: "ptt_start", tg: tg, callsign: callsign }.to_json)
    # Update metadata to reflect the browser that is actually transmitting
    update_web_node_info(redis, callsign)
    # Optimistic broadcast so dashboard cards update instantly
    broadcast_talker_hint(callsign, tg, true)
  ensure
    redis&.close
  end

  def ptt_stop(data)
    tg = data["tg"].to_i
    return unless tg > 0

    callsign = data["callsign"].to_s.strip.upcase
    redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
    redis.publish("audio:tx", { action: "ptt_stop", tg: tg, callsign: callsign }.to_json)
    broadcast_talker_hint(callsign, tg, false)
  ensure
    redis&.close
  end

  def tx_audio(data)
    return if data["audio"].blank?

    redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
    callsign = (data["callsign"].presence || @web_callsign).to_s.strip.upcase
    redis.publish("audio:tx", { action: "audio", audio: data["audio"], callsign: callsign }.to_json)
  ensure
    redis&.close
  end

  private

  def broadcast_talker_hint(callsign, tg, talking)
    patch = { "callsign" => callsign, "isTalker" => talking, "tg" => tg }
    { "sw" => params[:sw].to_s, "swVer" => params[:sw_ver].to_s,
      "nodeLocation" => params[:node_location].to_s }.each do |k, v|
      patch[k] = v unless v.empty?
    end
    ActionCable.server.broadcast("updates", patch)
  end

  def update_web_node_info(redis, callsign)
    meta = { sw: params[:sw].to_s, swVer: params[:sw_ver].to_s,
             nodeClass: params[:node_class].to_s, nodeLocation: params[:node_location].to_s,
             sysop: params[:sysop].to_s }.reject { |_, v| v.blank? }
    redis.hset("web_node_info", callsign, meta.to_json) if meta.any?
  end
end
