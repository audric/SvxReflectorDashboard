require "mqtt"
require "timeout"

module Admin
  class MqttController < ApplicationController
    layout false
    before_action :require_admin

    # GET /admin/mqtt.json — broker info + recent messages
    # Subscribes to # for up to 2 seconds, collects whatever arrives.
    def show
      cfg = mqtt_config
      unless cfg[:host].present?
        return render json: { error: "MQTT not configured" }, status: :unprocessable_entity
      end

      messages = []
      sys_topics = {}
      error = nil

      begin
        client = MQTT::Client.new(
          host: cfg[:host],
          port: cfg[:port].to_i,
          username: cfg[:username].presence,
          password: cfg[:password].presence,
          ssl: cfg[:tls_enabled] == "1"
        )
        client.connect

        # Subscribe to everything
        client.subscribe("#", "$SYS/#")

        # Collect messages for up to 2 seconds using Timeout
        Timeout.timeout(2) do
          loop do
            topic, payload = client.get
            if topic.start_with?("$SYS/")
              sys_topics[topic] = payload
            else
              messages << { topic: topic, payload: payload, ts: Time.now.iso8601(3) }
            end
          end
        end
      rescue Timeout::Error
        # Expected — collection window expired
      rescue => e
        error = "#{e.class}: #{e.message}"
      ensure
        client&.disconnect rescue nil
      end

      render json: {
        config: { host: cfg[:host], port: cfg[:port], topic_prefix: cfg[:topic_prefix] },
        sys: sys_topics,
        messages: messages,
        error: error
      }
    end

    private

    def mqtt_config
      config = ReflectorConfig.load
      {
        host: config.mqtt["HOST"],
        port: config.mqtt["PORT"] || "1883",
        username: config.mqtt["USERNAME"],
        password: config.mqtt["PASSWORD"],
        topic_prefix: config.mqtt["TOPIC_PREFIX"],
        tls_enabled: config.mqtt["TLS_ENABLED"]
      }
    end
  end
end
