# frozen_string_literal: true

# HTTP polling source for ReflectorListener.
# Fetches /status from the reflector's HTTP API on a configurable interval
# and feeds each snapshot into the shared processing pipeline.
class ReflectorListener
  module HttpSource
    # Runs the HTTP poll loop (blocking). Call from a thread.
    def self.run
      STDERR.puts "[HttpSource] Starting HTTP poll loop"

      loop do
        begin
          status_url = Setting.get('reflector_status_url',
                        ENV.fetch('REFLECTOR_STATUS_URL', 'http://213.254.10.33:8181/status'))
          res = ReflectorListener.http_get(status_url)
          status_data = JSON.parse(res.body)

          ReflectorListener.process_snapshot(status_data)
        rescue => e
          STDERR.puts "[HttpSource] Error: #{e.message}"
        end

        interval = (Setting.get('poll_interval') || ReflectorListener::POLL_INTERVAL).to_i.clamp(1, 10)
        sleep interval
      end
    end
  end
end
