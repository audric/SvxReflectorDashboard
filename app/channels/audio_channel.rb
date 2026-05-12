class AudioChannel < ApplicationCable::Channel
  MAX_OPUS_B64_SIZE = 700   # ~500 bytes decoded — generous limit for a 20ms Opus frame
  TX_FRAME_INTERVAL = 0.018 # minimum seconds between TX frames (~55 fps max)

  def subscribed
    unless current_user&.can_monitor? && current_user&.reflector_auth_key.present?
      reject
      return
    end

    tg = params[:tg].to_i
    if tg > 0
      callsign = "#{current_user.callsign.upcase}-WEB"
      auth_key = current_user.reflector_auth_key.to_s
      @web_callsign = callsign
      @web_tg = tg
      @last_tx_at = 0.0

      stream_from "audio:tg:#{tg}"
      stream_from "audio:user:#{callsign}"

      ref_key = "web_node_refs:#{callsign}"
      redis.incr(ref_key)
      redis.expire(ref_key, 120)
      # Always publish connect — the audio_bridge deduplicates via session
      # refCount. Skipping on ref > 1 caused stale keys (after audio_bridge
      # restart) to silently prevent new sessions from being created.
      redis.publish("audio:commands", {
        action: "connect", tg: tg, callsign: callsign, auth_key: auth_key,
        sw: params[:sw].to_s, sw_ver: params[:sw_ver].to_s,
        node_class: params[:node_class].to_s, node_location: params[:node_location].to_s,
        sysop: params[:sysop].to_s
      }.to_json)
      update_web_node_info(callsign)
      @ref_key = ref_key
    else
      reject
    end
  end

  def unsubscribed
    return if @web_callsign.blank?

    ref = redis.decr("web_node_refs:#{@web_callsign}")
    # Always publish disconnect so the bridge refCount stays in sync
    # (subscribed always publishes connect, so we must always publish disconnect)
    redis.publish("audio:commands", { action: "disconnect", callsign: @web_callsign }.to_json)
    if ref <= 0
      redis.del("web_node_refs:#{@web_callsign}")
      redis.hdel("web_node_info", @web_callsign)
    end
  ensure
    @redis&.close
    @redis = nil
  end

  def select_tg(data)
    tg = data["tg"].to_i
    return unless tg > 0

    refresh_ttl
    redis.publish("audio:commands", { action: "select_tg", tg: tg, callsign: @web_callsign }.to_json)
  end

  def ptt_start(data)
    return unless current_user&.can_transmit?

    tg = data["tg"].to_i
    return unless tg > 0

    refresh_ttl
    redis.publish("audio:tx", { action: "ptt_start", tg: tg, callsign: @web_callsign }.to_json)
    update_web_node_info(@web_callsign)
    broadcast_talker_hint(@web_callsign, tg, true)
  end

  def ptt_stop(data)
    return unless current_user&.can_transmit?

    tg = data["tg"].to_i
    return unless tg > 0

    refresh_ttl
    redis.publish("audio:tx", { action: "ptt_stop", tg: tg, callsign: @web_callsign }.to_json)
    broadcast_talker_hint(@web_callsign, tg, false)
  end

  def tx_audio(data)
    return unless current_user&.can_transmit?

    audio = data["audio"].to_s
    return if audio.blank?

    # Validate frame size — legitimate Opus frames are small
    if audio.bytesize > MAX_OPUS_B64_SIZE
      Rails.logger.warn("AudioChannel: oversized TX frame from #{@web_callsign} (#{audio.bytesize} bytes), dropped")
      return
    end

    # Rate limit — drop frames that arrive too fast
    now = Process.clock_gettime(Process::CLOCK_MONOTONIC)
    if now - @last_tx_at < TX_FRAME_INTERVAL
      return
    end
    @last_tx_at = now

    redis.publish("audio:tx", { action: "audio", audio: audio, callsign: @web_callsign }.to_json)
  end

  def keepalive(_data = nil)
    refresh_ttl
  end

  private

  # One Redis client per channel instance (== one WebSocket connection).
  # Reused across every method invocation; closed in unsubscribed. Replaces the
  # previous per-call `Redis.new`/`close` pattern that was leaking T_DATA in the
  # hot tx_audio path (~50 frames/sec).
  def redis
    @redis ||= Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
  end

  def refresh_ttl
    return if @ref_key.blank?
    redis.expire(@ref_key, 120)
  end

  def broadcast_talker_hint(callsign, tg, talking)
    patch = { "callsign" => callsign, "isTalker" => talking, "tg" => tg }
    { "sw" => params[:sw].to_s, "swVer" => params[:sw_ver].to_s,
      "nodeLocation" => params[:node_location].to_s }.each do |k, v|
      patch[k] = v unless v.empty?
    end
    ActionCable.server.broadcast("updates", patch)
  end

  def update_web_node_info(callsign)
    return if callsign.blank?
    meta = { sw: params[:sw].to_s, swVer: params[:sw_ver].to_s,
             nodeClass: params[:node_class].to_s, nodeLocation: params[:node_location].to_s,
             sysop: params[:sysop].to_s }.reject { |_, v| v.blank? }
    redis.hset("web_node_info", callsign, meta.to_json) if meta.any?
  end
end
