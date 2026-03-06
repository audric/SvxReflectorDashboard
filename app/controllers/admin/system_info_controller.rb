module Admin
  class SystemInfoController < ApplicationController
    layout false
    before_action :require_admin

    DOCKER_SOCKET = "/var/run/docker.sock"

    def show
      @info = {
        "Ruby" => RUBY_VERSION,
        "Rails" => Rails::VERSION::STRING,
        "Puma" => Puma::Const::PUMA_VERSION,
        "SQLite" => ActiveRecord::Base.connection.execute("SELECT sqlite_version()").first["sqlite_version()"],
        "Redis" => redis_version,
        "Environment" => Rails.env,
        "Database" => ActiveRecord::Base.connection_db_config.database,
        "Database size" => format_bytes(database_size),
        "Node events" => NodeEvent.count,
        "Registered users" => User.count,
        "Server time" => Time.current.strftime("%Y-%m-%d %H:%M:%S %Z"),
      }
      @services = fetch_docker_services
    end

    private

    def redis_version
      Redis.new(url: ENV.fetch("REDIS_URL", "redis://redis:6379/1")).info["redis_version"]
    rescue => e
      "unavailable (#{e.message})"
    end

    def database_size
      db_path = ActiveRecord::Base.connection_db_config.database
      File.exist?(db_path) ? File.size(db_path) : 0
    end

    def format_bytes(bytes)
      return "0 B" if bytes == 0
      units = %w[B KB MB GB]
      exp = (Math.log(bytes) / Math.log(1024)).to_i
      exp = units.size - 1 if exp >= units.size
      "%.1f %s" % [bytes.to_f / 1024**exp, units[exp]]
    end

    def fetch_docker_services
      return [] unless File.exist?(DOCKER_SOCKET)

      require "socket"
      require "json"

      sock = UNIXSocket.new(DOCKER_SOCKET)
      sock.write("GET /containers/json?all=true HTTP/1.0\r\nHost: localhost\r\n\r\n")
      response = sock.read
      sock.close

      body = response.split("\r\n\r\n", 2).last
      containers = JSON.parse(body)

      # Find our compose project by matching the project label from any container that has one
      project = containers.filter_map { |c| c.dig("Labels", "com.docker.compose.project") }.first
      return [] unless project

      containers
        .select { |c| c.dig("Labels", "com.docker.compose.project") == project }
        .reject { |c| c.dig("Labels", "com.docker.compose.oneoff") == "True" }
        .sort_by { |c| c.dig("Labels", "com.docker.compose.service") || "" }
        .map { |c| parse_container(c) }
    rescue => e
      Rails.logger.warn("Docker socket query failed: #{e.message}")
      []
    end

    def parse_container(c)
      state = c["State"]        # running, exited, etc.
      status = c["Status"]      # "Up 2 hours", "Exited (0) 3 minutes ago", etc.
      service = c.dig("Labels", "com.docker.compose.service") || "unknown"
      image = c["Image"]
      created = Time.at(c["Created"]).strftime("%Y-%m-%d %H:%M") rescue nil

      {
        service: service,
        image: image,
        state: state,
        status: status,
        created: created,
      }
    end

  end
end
