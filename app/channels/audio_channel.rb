class AudioChannel < ApplicationCable::Channel
  def subscribed
    tg = params[:tg].to_i
    if tg > 0
      stream_from "audio:tg:#{tg}"
      callsign = params[:callsign].to_s.strip.upcase
      auth_key = params[:auth_key].to_s
      @web_callsign = callsign
      redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
      redis.publish("audio:commands", {
        action: "connect", tg: tg, callsign: callsign, auth_key: auth_key,
        sw: params[:sw].to_s, sw_ver: params[:sw_ver].to_s,
        node_class: params[:node_class].to_s, node_location: params[:node_location].to_s,
        sysop: params[:sysop].to_s
      }.to_json)
      # Store metadata so the poller can enrich the reflector snapshot
      meta = { sw: params[:sw].to_s, swVer: params[:sw_ver].to_s,
               nodeClass: params[:node_class].to_s, nodeLocation: params[:node_location].to_s,
               sysop: params[:sysop].to_s }.reject { |_, v| v.blank? }
      redis.hset("web_node_info", callsign, meta.to_json) if meta.any?
      redis.close
    else
      reject
    end
  end

  def unsubscribed
    redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
    redis.publish("audio:commands", { action: "disconnect", callsign: @web_callsign }.to_json)
    redis.hdel("web_node_info", @web_callsign) if @web_callsign.present?
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
  ensure
    redis&.close
  end

  def ptt_stop(data)
    tg = data["tg"].to_i
    return unless tg > 0

    callsign = data["callsign"].to_s.strip.upcase
    redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
    redis.publish("audio:tx", { action: "ptt_stop", tg: tg, callsign: callsign }.to_json)
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
end
