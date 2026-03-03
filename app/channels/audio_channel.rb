class AudioChannel < ApplicationCable::Channel
  def subscribed
    tg = params[:tg].to_i
    if tg > 0
      stream_from "audio:tg:#{tg}"
      callsign = params[:callsign].to_s.strip.upcase
      auth_key = params[:auth_key].to_s
      redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
      redis.publish("audio:commands", { action: "connect", tg: tg, callsign: callsign, auth_key: auth_key }.to_json)
      redis.close
    else
      reject
    end
  end

  def unsubscribed
    redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
    redis.publish("audio:commands", { action: "disconnect" }.to_json)
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
    redis.publish("audio:tx", { action: "audio", audio: data["audio"] }.to_json)
  ensure
    redis&.close
  end
end
