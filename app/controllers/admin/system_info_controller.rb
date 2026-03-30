module Admin
  class SystemInfoController < ApplicationController
    layout false
    before_action :require_admin

    DOCKER_SOCKET = "/var/run/docker.sock"
    BOOT_ORDER = %w[init-reflector-conf redis svxreflector web updater audio_bridge caddy].freeze
    SERVICE_DEPS = {
      "init-reflector-conf" => [],
      "redis" => [],
      "svxreflector" => %w[init-reflector-conf],
      "web" => %w[init-reflector-conf redis svxreflector],
      "updater" => %w[web redis svxreflector],
      "audio_bridge" => %w[redis svxreflector],
      "caddy" => %w[web],
    }.freeze

    def show
      @active_tab = params[:tab] || "info"

      # For Settings tab
      settings_keys = Admin::SettingsController::KEYS
      settings_defaults = { "reflector_status_url" => ENV.fetch("REFLECTOR_STATUS_URL", ""), "brand_name" => ENV.fetch("BRAND_NAME", ""), "poll_interval" => "4" }
      @settings = settings_keys.index_with { |key| Setting.get(key, settings_defaults[key]) }

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
      @host = fetch_host_info
      @memory = fetch_memory_info
      @disks = fetch_disk_info
      @services = fetch_docker_services
      @service_graph = build_mermaid_graph

      # For Logs tab
      @containers = fetch_log_containers
    end

    private

    def fetch_log_containers
      return [] unless File.exist?(DOCKER_SOCKET)
      containers = docker_api_get("/containers/json?all=true")
      return [] unless containers

      project = containers.filter_map { |c| c.dig("Labels", "com.docker.compose.project").presence }.first
      bridge_ids = Set.new(
        containers
          .select { |c| c["Names"]&.any? { |n| n =~ /\A\/(?:svxlink|xlx|dmr|ysf|allstar|zello|iax|sip)-bridge-\d+\z/ } }
          .map { |c| c["Id"] }
      )

      results = containers
        .select { |c| c.dig("Labels", "com.docker.compose.project") == project }
        .reject { |c| c.dig("Labels", "com.docker.compose.service")&.start_with?("init-") }
        .reject { |c| bridge_ids.include?(c["Id"]) }
        .map { |c| { id: c["Id"], name: c.dig("Labels", "com.docker.compose.service") || c["Names"]&.first&.delete_prefix("/"), state: c["State"] } }

      bridge_names = Bridge.pluck(:id, :name).to_h
      containers
        .select { |c| c["Names"]&.any? { |n| n =~ /\A\/(?:svxlink|xlx|dmr|ysf|allstar|zello|iax|sip)-bridge-\d+\z/ } }
        .each { |c|
          container_name = c["Names"].find { |n| n =~ /(?:svxlink|xlx|dmr|ysf|allstar|zello|iax|sip)-bridge/ }&.delete_prefix("/")
          bridge_id = container_name&.match(/(\d+)\z/)&.[](1)&.to_i
          label = bridge_names[bridge_id] ? "bridge: #{bridge_names[bridge_id]}" : container_name
          results << { id: c["Id"], name: label, state: c["State"] }
        }
      results.sort_by { |c| c[:name].to_s }
    rescue => e
      Rails.logger.warn("Docker containers fetch failed: #{e.message}")
      []
    end

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

    def docker_api_get(path)
      require "socket"
      require "json"
      return nil unless File.exist?(DOCKER_SOCKET)

      sock = UNIXSocket.new(DOCKER_SOCKET)
      sock.write("GET #{path} HTTP/1.0\r\nHost: localhost\r\n\r\n")
      response = sock.read
      sock.close
      JSON.parse(response.split("\r\n\r\n", 2).last)
    end

    def fetch_host_info
      info = docker_api_get("/info")
      return {} unless info

      uptime_secs = File.read("/proc/uptime").split.first.to_f rescue 0
      days = (uptime_secs / 86400).to_i
      hours = ((uptime_secs % 86400) / 3600).to_i
      mins = ((uptime_secs % 3600) / 60).to_i
      uptime_str = [("#{"#{days}d" if days > 0}"), "#{"#{hours}h" if hours > 0}", "#{mins}m"].compact_blank.join(" ")

      {
        "Hostname" => info["Name"],
        "OS" => info["OperatingSystem"],
        "Kernel" => info["KernelVersion"],
        "Architecture" => info["Architecture"],
        "CPUs" => info["NCPU"],
        "Docker" => info["ServerVersion"],
        "Host uptime" => uptime_str,
        "Containers" => "#{info["ContainersRunning"]} running / #{info["Containers"]} total",
        "Images" => info["Images"],
      }
    rescue => e
      Rails.logger.warn("Docker host info failed: #{e.message}")
      {}
    end

    def fetch_memory_info
      meminfo = {}
      File.readlines("/proc/meminfo").each do |line|
        key, val = line.split(":")
        meminfo[key.strip] = val.strip.split.first.to_i * 1024 # kB → bytes
      end

      total = meminfo["MemTotal"]
      free = meminfo["MemFree"]
      available = meminfo["MemAvailable"]
      buffers = meminfo["Buffers"]
      cached = meminfo["Cached"]
      used = total - free - buffers - cached
      swap_total = meminfo["SwapTotal"]
      swap_free = meminfo["SwapFree"]
      swap_used = swap_total - swap_free

      used_pct = (used.to_f / total * 100).round(1)
      bufcache_pct = ((buffers + cached).to_f / total * 100).round(1)
      swap_pct = swap_total > 0 ? (swap_used.to_f / swap_total * 100).round(1) : 0

      {
        used_pct: used_pct, bufcache_pct: bufcache_pct, swap_pct: swap_pct,
        used: format_bytes(used), total: format_bytes(total),
        buffers: format_bytes(buffers), cached: format_bytes(cached),
        available: format_bytes(available), free: format_bytes(free),
        swap_total: format_bytes(swap_total), swap_used: format_bytes(swap_used),
        has_swap: swap_total > 0,
      }
    rescue => e
      Rails.logger.warn("Memory info failed: #{e.message}")
      nil
    end

    def fetch_disk_info
      vfs = %w[tmpfs devtmpfs squashfs proc sysfs cgroup cgroup2 devpts mqueue hugetlbfs autofs securityfs pstore debugfs tracefs fusectl configfs binfmt_misc nsfs]
      lines = `df -T -B1 2>/dev/null`.lines.drop(1)
      seen = Set.new
      lines.filter_map { |line|
        cols = line.split
        next if cols.size < 7
        device, fstype, total, used, avail, _pct, mount = cols
        next if vfs.include?(fstype)
        next if seen.include?(device)
        seen << device

        total_b = total.to_i
        used_b = used.to_i
        used_pct = total_b > 0 ? (used_b.to_f / total_b * 100).round(1) : 0

        {
          device: device, fstype: fstype, mount: mount,
          total: format_bytes(total_b), used: format_bytes(used_b),
          available: format_bytes(avail.to_i), used_pct: used_pct,
        }
      }
    rescue => e
      Rails.logger.warn("Disk info failed: #{e.message}")
      []
    end

    def fetch_docker_services
      containers = docker_api_get("/containers/json?all=true")
      return [] unless containers

      # Find our compose project by matching the project label from any container that has one
      project = containers.filter_map { |c| c.dig("Labels", "com.docker.compose.project").presence }.first
      return [] unless project

      bridge_ids = Set.new(
        containers
          .select { |c| c["Names"]&.any? { |n| n =~ /\A\/(?:svxlink|xlx|dmr|ysf|allstar|zello|iax|sip)-bridge-\d+\z/ } }
          .map { |c| c["Id"] }
      )

      compose_services = containers
        .select { |c| c.dig("Labels", "com.docker.compose.project") == project }
        .reject { |c| c.dig("Labels", "com.docker.compose.oneoff") == "True" }
        .reject { |c| bridge_ids.include?(c["Id"]) }
        .sort_by { |c| BOOT_ORDER.index(c.dig("Labels", "com.docker.compose.service")) || 999 }
        .map { |c| parse_container(c) }

      # Include dynamically-created bridge containers with friendly names
      bridge_names = Bridge.pluck(:id, :name).to_h
      bridge_containers = containers
        .select { |c| c["Names"]&.any? { |n| n =~ /\A\/(?:svxlink|xlx|dmr|ysf|allstar|zello|iax|sip)-bridge-\d+\z/ } }
        .map { |c|
          container_name = c["Names"].find { |n| n =~ /(?:svxlink|xlx|dmr|ysf|allstar|zello|iax|sip)-bridge/ }&.delete_prefix("/")
          bridge_id = container_name&.match(/(\d+)\z/)&.[](1)&.to_i
          label = bridge_names[bridge_id] ? "bridge: #{bridge_names[bridge_id]}" : container_name
          { service: label, image: c["Image"], state: c["State"], status: c["Status"],
            created: (Time.at(c["Created"]).strftime("%Y-%m-%d %H:%M") rescue nil) }
        }

      compose_services + bridge_containers
    rescue => e
      Rails.logger.warn("Docker socket query failed: #{e.message}")
      []
    end

    def build_mermaid_graph
      state_map = @services.each_with_object({}) { |s, h| h[s[:service]] = s[:state] }
      lines = ["graph LR"]
      SERVICE_DEPS.each do |svc, deps|
        id = svc.tr("-", "_")
        lines << "  #{id}[\"#{svc}\"]"
        deps.each do |dep|
          dep_id = dep.tr("-", "_")
          lines << "  #{dep_id} --> #{id}"
        end
      end

      # Add dynamic bridge nodes from database
      Bridge.find_each do |bridge|
        id = bridge.container_name.tr("-", "_")
        lines << "  #{id}[\"#{bridge.name}\"]"
        lines << "  svxreflector --> #{id}"
      end

      # Style all nodes by state
      all_services = SERVICE_DEPS.keys + Bridge.all.map(&:container_name)
      all_services.each do |svc|
        id = svc.tr("-", "_")
        case state_map[svc]
        when "running"
          lines << "  style #{id} fill:#238636,stroke:#3fb950,color:#fff"
        when "exited"
          lines << "  style #{id} fill:#6e2b2b,stroke:#f85149,color:#fff"
        else
          lines << "  style #{id} fill:#30363d,stroke:#6e7681,color:#8b949e" unless state_map[svc]
        end
      end
      lines.join("\n")
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
