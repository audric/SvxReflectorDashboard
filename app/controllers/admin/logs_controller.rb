module Admin
  class LogsController < ApplicationController
    layout false
    before_action :require_admin

    DOCKER_SOCKET = "/var/run/docker.sock"

    def show
      @containers = fetch_containers
    end

    def fetch
      container_id = params[:container].to_s
      unless container_id.match?(/\A[a-f0-9]{12,64}\z/)
        render json: { logs: "Invalid container ID" }, status: :bad_request
        return
      end

      tail = (params[:tail] || 200).to_i.clamp(1, 1000)
      since = params[:since] # ISO 8601 timestamp for incremental fetching
      output = fetch_container_logs(container_id, tail, since)
      render json: { logs: output }
    end

    private

    def fetch_containers
      return [] unless File.exist?(DOCKER_SOCKET)

      containers = docker_api_get("/containers/json?all=true")
      return [] unless containers

      project = containers.filter_map { |c| c.dig("Labels", "com.docker.compose.project").presence }.first

      # Compose services (exclude init containers and bridge containers)
      bridge_ids = Set.new(
        containers
          .select { |c| c["Names"]&.any? { |n| n =~ /\A\/svxlink-bridge-\d+\z/ } }
          .map { |c| c["Id"] }
      )

      results = containers
        .select { |c| c.dig("Labels", "com.docker.compose.project") == project }
        .reject { |c| c.dig("Labels", "com.docker.compose.service")&.start_with?("init-") }
        .reject { |c| bridge_ids.include?(c["Id"]) }
        .map { |c| { id: c["Id"], name: c.dig("Labels", "com.docker.compose.service") || c["Names"]&.first&.delete_prefix("/"), state: c["State"] } }

      # Bridge containers with friendly names from DB
      bridge_names = Bridge.pluck(:id, :name).to_h
      containers
        .select { |c| c["Names"]&.any? { |n| n =~ /\A\/svxlink-bridge-\d+\z/ } }
        .each { |c|
          container_name = c["Names"].find { |n| n =~ /svxlink-bridge/ }&.delete_prefix("/")
          bridge_id = container_name&.match(/(\d+)\z/)&.[](1)&.to_i
          label = bridge_names[bridge_id] ? "bridge: #{bridge_names[bridge_id]}" : container_name
          results << { id: c["Id"], name: label, state: c["State"] }
        }

      results.sort_by { |c| c[:name].to_s }
    rescue => e
      Rails.logger.warn("Docker containers fetch failed: #{e.message}")
      []
    end

    def fetch_container_logs(container_id, tail, since = nil)
      return "Docker socket not available" unless File.exist?(DOCKER_SOCKET)

      query = "stdout=true&stderr=true&timestamps=true"
      if since.present?
        # Parse ISO 8601 timestamp and add 1ns to avoid re-fetching the last line
        t = Time.iso8601(since) + Rational(1, 1_000_000_000)
        query += "&since=#{t.to_f}"
      else
        query += "&tail=#{tail}"
      end

      sock = UNIXSocket.new(DOCKER_SOCKET)
      sock.write("GET /containers/#{container_id}/logs?#{query} HTTP/1.0\r\nHost: localhost\r\n\r\n")
      response = sock.read
      sock.close

      body = response.split("\r\n\r\n", 2).last

      # Docker multiplexed stream: each frame has 8-byte header (stream type + size)
      lines = []
      pos = 0
      while pos + 8 <= body.bytesize
        frame_size = body.byteslice(pos + 4, 4).unpack1("N")
        break if pos + 8 + frame_size > body.bytesize
        lines << body.byteslice(pos + 8, frame_size)
        pos += 8 + frame_size
      end
      # Fallback: if no frames parsed, return raw (container may use tty mode)
      lines.empty? ? body.force_encoding("UTF-8").scrub : lines.join.force_encoding("UTF-8").scrub
    rescue => e
      "Error fetching logs: #{e.class} #{e.message}"
    end

    def docker_api_get(path)
      sock = UNIXSocket.new(DOCKER_SOCKET)
      sock.write("GET #{path} HTTP/1.0\r\nHost: localhost\r\n\r\n")
      response = sock.read
      sock.close
      JSON.parse(response.split("\r\n\r\n", 2).last)
    end
  end
end
