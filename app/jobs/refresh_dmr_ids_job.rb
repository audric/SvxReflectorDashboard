require "net/http"
require "tempfile"

# Loads the radioid.net DMR user database into a Redis hash (DMR ID =>
# callsign) so every consumer shares one copy. Scheduled nightly; a second
# schedule runs it in "ensure" mode to fill the table when it is missing
# (fresh install, Redis restarted, first DMR bridge just created).
class RefreshDmrIdsJob < ApplicationJob
  queue_as :default

  SOURCE_URL  = "https://radioid.net/static/user.csv"
  REDIS_KEY   = "dmr:ids"
  UPDATED_KEY = "dmr:ids:updated_at"
  MIN_ENTRIES = 10_000
  BATCH_SIZE  = 5_000

  def perform(mode = "refresh")
    return unless Bridge.exists?(bridge_type: "dmr")

    redis = Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1"))
    return if mode == "ensure" && redis.exists?(REDIS_KEY)

    start = Time.now
    count = download { |file| load_into_redis(redis, file) }
    Rails.logger.info("[RefreshDmrIdsJob] loaded=#{count} elapsed=#{(Time.now - start).round(1)}s")
  ensure
    redis&.close
  end

  private

  def download
    uri = URI(SOURCE_URL)
    Tempfile.create("dmrids") do |file|
      file.binmode
      Net::HTTP.start(uri.host, uri.port, use_ssl: true, open_timeout: 15, read_timeout: 60) do |http|
        http.request_get(uri.request_uri, "User-Agent" => "SvxReflectorDashboard") do |res|
          raise "HTTP #{res.code} from #{uri.host}" unless res.is_a?(Net::HTTPSuccess)
          res.read_body { |chunk| file.write(chunk) }
        end
      end
      file.rewind
      yield file
    end
  end

  # Builds the table under a temporary key and swaps it in with RENAME, so
  # readers never see a partial table and a bad download never replaces a
  # good one.
  def load_into_redis(redis, file)
    tmp_key = "#{REDIS_KEY}:tmp"
    redis.del(tmp_key)

    count = 0
    batch = {}
    file.each_line.with_index do |line, i|
      next if i.zero? # header row
      id, callsign = line.scrub("").split(",", 3)
      callsign = callsign.to_s.strip.upcase
      next unless id.to_s.match?(/\A\d+\z/) && callsign.match?(/\A[A-Z0-9\/-]{3,12}\z/)
      batch[id] = callsign
      next if batch.size < BATCH_SIZE
      redis.hset(tmp_key, batch)
      count += batch.size
      batch = {}
    end
    unless batch.empty?
      redis.hset(tmp_key, batch)
      count += batch.size
    end

    if count < MIN_ENTRIES
      redis.del(tmp_key)
      raise "only #{count} entries parsed, keeping the existing table"
    end

    redis.rename(tmp_key, REDIS_KEY)
    redis.set(UPDATED_KEY, Time.now.utc.iso8601)
    count
  end
end
