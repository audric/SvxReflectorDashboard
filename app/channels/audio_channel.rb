class AudioChannel < ApplicationCable::Channel
  def subscribed
    tg = params[:tg].to_i
    if tg > 0
      stream_from "audio:tg:#{tg}"
      # Tell the audio bridge to switch to this TG
      redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
      redis.publish("audio:commands", { action: "select_tg", tg: tg }.to_json)
      redis.close
    else
      reject
    end
  end

  def select_tg(data)
    tg = data["tg"].to_i
    return unless tg > 0

    redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
    redis.publish("audio:commands", { action: "select_tg", tg: tg }.to_json)
  ensure
    redis&.close
  end
end
