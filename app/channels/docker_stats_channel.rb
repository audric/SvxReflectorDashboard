class DockerStatsChannel < ApplicationCable::Channel
  DOCKER_SOCKET = "/var/run/docker.sock"
  INTERVAL = 5 # seconds
  BOOT_ORDER = %w[init-reflector-conf redis svxreflector web updater audio_bridge caddy].freeze
  BRIDGE_RE = /\A(?:svxlink|xlx|dmr|ysf|allstar)-bridge-(\d+)\z/

  periodically :broadcast_stats, every: INTERVAL

  def subscribed
    stream_from "docker_stats"
    # Send first snapshot immediately
    transmit({ stats: fetch_all_stats })
  end

  private

  def broadcast_stats
    ActionCable.server.broadcast("docker_stats", { stats: fetch_all_stats })
  rescue => e
    Rails.logger.warn("DockerStatsChannel error: #{e.message}")
  end

  def fetch_all_stats
    containers = docker_api_get("/containers/json?all=true")
    return [] unless containers

    bridge_names = Bridge.pluck(:id, :name).to_h rescue {}
    project = containers.filter_map { |c| c.dig("Labels", "com.docker.compose.project").presence }.first

    # Separate compose services from bridge containers
    bridge_ids = Set.new(
      containers
        .select { |c| c["Names"]&.first&.delete_prefix("/") =~ BRIDGE_RE }
        .map { |c| c["Id"] }
    )

    compose_containers = containers
      .select { |c| c.dig("Labels", "com.docker.compose.project") == project }
      .reject { |c| c.dig("Labels", "com.docker.compose.oneoff") == "True" }
      .reject { |c| bridge_ids.include?(c["Id"]) }
      .reject { |c| c.dig("Labels", "com.docker.compose.service")&.start_with?("init-") }
      .sort_by { |c| BOOT_ORDER.index(c.dig("Labels", "com.docker.compose.service")) || 999 }

    bridge_containers = containers
      .select { |c| c["Names"]&.first&.delete_prefix("/") =~ BRIDGE_RE }
      .sort_by { |c| c["Names"]&.first.to_s }

    # Fetch stats in parallel for running containers
    mutex = Mutex.new
    stats_by_id = {}
    ordered = compose_containers + bridge_containers

    threads = ordered.select { |c| c["State"] == "running" }.map { |c|
      Thread.new {
        raw = docker_api_get("/containers/#{c["Id"]}/stats?stream=false&one-shot=true")
        next unless raw
        name = resolve_container_name(c, bridge_names)
        entry = build_stats_entry(name, c["Id"], c["State"], raw)
        mutex.synchronize { stats_by_id[c["Id"]] = entry }
      }
    }
    threads.each(&:join)

    # Build final list preserving order: compose services first, then bridges
    ordered.filter_map do |c|
      if stats_by_id[c["Id"]]
        stats_by_id[c["Id"]]
      elsif bridge_ids.include?(c["Id"])
        # Stopped bridge — include with dashes
        name = resolve_container_name(c, bridge_names)
        { name: name, id: c["Id"][0..11], state: c["State"],
          cpu_pct: 0, mem_used: 0, mem_limit: 0, mem_pct: 0,
          net_rx: 0, net_tx: 0, blk_read: 0, blk_write: 0 }
      end
    end
  end

  def resolve_container_name(container, bridge_names)
    name = container["Names"]&.first&.delete_prefix("/") || "unknown"
    if name =~ /\A(?:svxlink|xlx|dmr|ysf|allstar)-bridge-(\d+)\z/
      bridge_id = $1.to_i
      bridge_names[bridge_id] ? "bridge: #{bridge_names[bridge_id]}" : name
    else
      name
    end
  end

  def build_stats_entry(name, id, state, raw)
    cpu_delta = raw.dig("cpu_stats", "cpu_usage", "total_usage").to_i -
                raw.dig("precpu_stats", "cpu_usage", "total_usage").to_i
    sys_delta = raw.dig("cpu_stats", "system_cpu_usage").to_i -
                raw.dig("precpu_stats", "system_cpu_usage").to_i
    num_cpus  = raw.dig("cpu_stats", "online_cpus") || 1
    cpu_pct   = sys_delta > 0 ? (cpu_delta.to_f / sys_delta * num_cpus * 100).round(2) : 0.0

    mem_usage = raw.dig("memory_stats", "usage").to_i
    mem_cache = raw.dig("memory_stats", "stats", "cache").to_i
    mem_used  = mem_usage - mem_cache
    mem_limit = raw.dig("memory_stats", "limit").to_i

    net_rx = net_tx = 0
    (raw["networks"] || {}).each_value do |iface|
      net_rx += iface["rx_bytes"].to_i
      net_tx += iface["tx_bytes"].to_i
    end

    blk_read = blk_write = 0
    (raw.dig("blkio_stats", "io_service_bytes_recursive") || []).each do |entry|
      case entry["op"]&.downcase
      when "read"  then blk_read  += entry["value"].to_i
      when "write" then blk_write += entry["value"].to_i
      end
    end

    {
      name: name, id: id[0..11], state: state,
      cpu_pct: cpu_pct,
      mem_used: mem_used, mem_limit: mem_limit,
      mem_pct: mem_limit > 0 ? (mem_used.to_f / mem_limit * 100).round(2) : 0,
      net_rx: net_rx, net_tx: net_tx,
      blk_read: blk_read, blk_write: blk_write,
    }
  end

  def docker_api_get(path)
    require "socket"
    require "set"
    return nil unless File.exist?(DOCKER_SOCKET)

    sock = UNIXSocket.new(DOCKER_SOCKET)
    sock.write("GET #{path} HTTP/1.0\r\nHost: localhost\r\n\r\n")
    response = sock.read
    sock.close
    JSON.parse(response.split("\r\n\r\n", 2).last)
  end
end
