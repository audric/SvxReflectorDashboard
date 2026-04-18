class ReflectorConfig
  attr_accessor :global, :root_ca, :issuing_ca, :server_cert, :users, :passwords,
                :tg_rules, :trunks, :satellite, :mqtt, :twin, :redis_section

  def initialize
    @global = {}
    @root_ca = {}
    @issuing_ca = {}
    @server_cert = {}
    @users = {}       # callsign => group
    @passwords = {}   # group => password
    @tg_rules = {}    # tg_number (Integer) => { "ALLOW" => ..., "AUTO_QSY_AFTER" => ..., "SHOW_ACTIVITY" => ..., "ALLOW_MONITOR" => ... }
    @trunks = {}      # trunk_name => { "HOST" => ..., "PORT" => ..., "SECRET" => ..., "REMOTE_PREFIX" => ... }
    @satellite = {}   # { "LISTEN_PORT" => ..., "SECRET" => ... }
    @mqtt = {}        # { "HOST" => ..., "PORT" => ..., "USERNAME" => ..., ... }
    @twin = {}        # { "HOST" => ..., "PORT" => ..., "SECRET" => ... }
    @redis_section = {} # { "HOST" => ..., "PORT" => ..., "PASSWORD" => ..., "DB" => ..., ... }
  end

  def self.config_path
    Rails.root.join("reflector", "svxreflector.conf")
  end

  # Returns true if the [REDIS] section is configured with at least a HOST or UNIX_SOCKET.
  def redis_mode?
    redis_section['HOST'].present? || redis_section['UNIX_SOCKET'].present?
  end

  def self.redis_mode?
    load.redis_mode?
  end

  # Builds a Redis connection to the reflector's config store Redis (NOT the dashboard Redis).
  # Returns nil if [REDIS] is not configured.
  def reflector_redis
    return nil unless redis_mode?
    url = if redis_section['UNIX_SOCKET'].present?
            "unix://#{redis_section['UNIX_SOCKET']}"
          else
            host = redis_section['HOST'] || '127.0.0.1'
            port = redis_section['PORT'] || '6379'
            password = redis_section['PASSWORD']
            db = redis_section['DB'] || '0'
            auth = password.present? ? ":#{password}@" : ""
            "redis://#{auth}#{host}:#{port}/#{db}"
          end
    Redis.new(url: url)
  end

  # Returns the key prefix for the reflector's config Redis keys.
  def redis_key_prefix
    redis_section['KEY_PREFIX'].to_s
  end

  # Builds a prefixed Redis key.
  def redis_key(suffix)
    redis_key_prefix.present? ? "#{redis_key_prefix}:#{suffix}" : suffix
  end

  def self.load(path = config_path)
    config = new
    return config unless File.exist?(path)

    current_section = nil
    File.readlines(path, chomp: true).each do |line|
      # Skip comments and blank lines
      next if line.strip.empty? || line.strip.start_with?("#")

      if line.strip =~ /\A\[(.+)\]\z/
        current_section = Regexp.last_match(1)
        # Pre-create empty TG rule entry so it survives even with no keys
        if current_section =~ /\ATG#(\d+)\z/
          config.tg_rules[Regexp.last_match(1).to_i] ||= {}
        end
        # Pre-create empty trunk entry
        if current_section =~ /\ATRUNK_\w+\z/
          config.trunks[current_section] ||= {}
        end
        next
      end

      key, value = line.strip.split("=", 2)
      next unless key && value

      key = key.strip
      value = value.strip

      case current_section
      when "GLOBAL"
        config.global[key] = value
      when "ROOT_CA"
        config.root_ca[key] = value
      when "ISSUING_CA"
        config.issuing_ca[key] = value
      when "SERVER_CERT"
        config.server_cert[key] = value
      when "USERS"
        config.users[key] = value
      when "PASSWORDS"
        config.passwords[key] = value
      when /\ATG#(\d+)\z/
        tg_num = Regexp.last_match(1).to_i
        config.tg_rules[tg_num] ||= {}
        config.tg_rules[tg_num][key] = value
      when /\ATRUNK_\w+\z/
        config.trunks[current_section] ||= {}
        config.trunks[current_section][key] = value
      when "SATELLITE"
        config.satellite[key] = value
      when "TWIN"
        config.twin[key] = value
      when "REDIS"
        config.redis_section[key] = value
      when "MQTT"
        config.mqtt[key] = value
      end
    end

    config.users = config.users.sort.to_h
    config.passwords = config.passwords.sort.to_h

    # When [REDIS] is active, load users and passwords from Redis instead
    if config.redis_mode?
      begin
        r = config.reflector_redis
        if r
          redis_users = {}
          r.scan_each(match: config.redis_key("user:*")) do |key|
            callsign = key.split(":").last
            data = r.hgetall(key)
            redis_users[callsign] = data["group"] || callsign if data["enabled"] != "0"
          end
          config.users = redis_users.sort.to_h

          redis_passwords = {}
          r.scan_each(match: config.redis_key("group:*")) do |key|
            name = key.split(":").last
            data = r.hgetall(key)
            redis_passwords[name] = data["password"] if data["password"].present?
          end
          config.passwords = redis_passwords.sort.to_h
        end
      rescue => e
        Rails.logger.warn "[ReflectorConfig] Failed to load users from Redis, falling back to conf file: #{e.message}"
      end
    end

    config
  end

  MAX_BACKUPS = 10

  def save(path = self.class.config_path)
    backup(path) if File.exist?(path)
    lines = []

    # GLOBAL
    lines << "[GLOBAL]"
    global.each { |k, v| lines << "#{k}=#{v}" }
    lines << ""

    # ROOT_CA
    if root_ca.any?
      lines << "[ROOT_CA]"
      root_ca.each { |k, v| lines << "#{k}=#{v}" }
      lines << ""
    end

    # ISSUING_CA
    if issuing_ca.any?
      lines << "[ISSUING_CA]"
      issuing_ca.each { |k, v| lines << "#{k}=#{v}" }
      lines << ""
    end

    # SERVER_CERT
    if server_cert.any?
      lines << "[SERVER_CERT]"
      server_cert.each { |k, v| lines << "#{k}=#{v}" }
      lines << ""
    end

    # USERS
    lines << "[USERS]"
    users.each { |k, v| lines << "#{k}=#{v}" }
    lines << ""

    # PASSWORDS
    lines << "[PASSWORDS]"
    passwords.each { |k, v| lines << "#{k}=#{v}" }

    # TRUNK sections (sorted by name)
    trunks.keys.sort.each do |trunk_name|
      lines << ""
      lines << "[#{trunk_name}]"
      trunks[trunk_name].each { |k, v| lines << "#{k}=#{v}" }
    end

    # SATELLITE section
    if satellite.any?
      lines << ""
      lines << "[SATELLITE]"
      satellite.each { |k, v| lines << "#{k}=#{v}" }
    end

    # TWIN section
    if twin.any?
      lines << ""
      lines << "[TWIN]"
      twin.each { |k, v| lines << "#{k}=#{v}" }
    end

    # REDIS section
    if redis_section.any?
      lines << ""
      lines << "[REDIS]"
      redis_section.each { |k, v| lines << "#{k}=#{v}" }
    end

    # MQTT section
    if mqtt.any?
      lines << ""
      lines << "[MQTT]"
      mqtt.each { |k, v| lines << "#{k}=#{v}" }
    end

    # TG rules sorted numerically
    tg_rules.keys.sort.each do |tg_num|
      lines << ""
      lines << "[TG##{tg_num}]"
      tg_rules[tg_num].each { |k, v| lines << "#{k}=#{v}" }
    end

    lines << ""
    File.write(path, lines.join("\n"))
  end

  # Syncs all users with a reflector_auth_key into the reflector's user store.
  # When [REDIS] is configured, writes to the reflector's config Redis.
  # Otherwise, writes to the [USERS]/[PASSWORDS] sections of svxreflector.conf.
  # Also sends CFG commands via the control pipe for immediate in-memory effect.
  def self.sync_web_users
    config = load

    # Build the desired state from the database
    desired = {}
    User.where.not(reflector_auth_key: [nil, ""]).where(can_monitor: true).find_each do |user|
      web_cs = "#{user.callsign.upcase}-WEB"
      desired[web_cs] = user.reflector_auth_key
    end

    if config.redis_mode?
      sync_web_users_redis(config, desired)
    else
      sync_web_users_conf(config, desired)
    end
  end

  # Sync web users to the reflector's config Redis store.
  def self.sync_web_users_redis(config, desired)
    r = config.reflector_redis
    return unless r

    changed = false
    cfg_commands = []

    # Scan existing -WEB user keys in Redis
    pattern = config.redis_key("user:*-WEB")
    existing_web = []
    r.scan_each(match: pattern) { |key| existing_web << key }

    # Add or update entries
    desired.each do |web_cs, auth_key|
      group = web_cs
      user_key = config.redis_key("user:#{web_cs}")
      group_key = config.redis_key("group:#{group}")
      current_group = r.hget(user_key, "group")
      current_password = r.hget(group_key, "password")
      unless current_group == group && current_password == auth_key
        r.hset(user_key, "group", group, "enabled", "1")
        r.hset(group_key, "password", auth_key)
        cfg_commands << "CFG USERS #{web_cs} #{group}"
        cfg_commands << "CFG PASSWORDS #{group} #{auth_key}"
        changed = true
      end
    end

    # Remove stale -WEB entries
    existing_web.each do |user_key|
      web_cs = user_key.split(":").last
      unless desired.key?(web_cs)
        group_key = config.redis_key("group:#{web_cs}")
        r.del(user_key)
        r.del(group_key)
        cfg_commands << "CFG USERS #{web_cs}"
        cfg_commands << "CFG PASSWORDS #{web_cs}"
        changed = true
      end
    end

    if changed
      send_cfg_commands(cfg_commands)
      Rails.logger.info "[ReflectorConfig] Synced #{desired.size} web user(s) to Redis (#{cfg_commands.size} CFG commands)"
    end

    changed
  end

  # Sync web users to the conf file (original behavior).
  def self.sync_web_users_conf(config, desired)
    existing_web_callsigns = config.users.keys.select { |k| k.end_with?("-WEB") }

    changed = false
    cfg_commands = []

    # Add or update entries
    desired.each do |web_cs, auth_key|
      group = web_cs
      unless config.users[web_cs] == group && config.passwords[group] == auth_key
        config.users[web_cs] = group
        config.passwords[group] = auth_key
        cfg_commands << "CFG USERS #{web_cs} #{group}"
        cfg_commands << "CFG PASSWORDS #{group} #{auth_key}"
        changed = true
      end
    end

    # Remove stale -WEB entries
    existing_web_callsigns.each do |web_cs|
      unless desired.key?(web_cs)
        config.users.delete(web_cs)
        config.passwords.delete(web_cs)
        cfg_commands << "CFG USERS #{web_cs}"
        cfg_commands << "CFG PASSWORDS #{web_cs}"
        changed = true
      end
    end

    if changed
      config.save
      send_cfg_commands(cfg_commands)
      Rails.logger.info "[ReflectorConfig] Synced #{desired.size} web user(s) (#{cfg_commands.size} CFG commands)"
    end

    changed
  end

  # Restarts the svxreflector container to pick up config changes.
  def self.restart_svxreflector
    require "socket"
    container = find_reflector_container
    return unless container

    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("POST /containers/#{container["Id"]}/restart?t=5 HTTP/1.0\r\nHost: localhost\r\nContent-Length: 0\r\n\r\n")
    sock.read
    sock.close
    Rails.logger.info "[ReflectorConfig] Restarted svxreflector"
  rescue => e
    Rails.logger.error "[ReflectorConfig] Failed to restart svxreflector: #{e.message}"
  end

  # Sends CFG commands to the running reflector via its control pipe.
  # Changes take effect immediately in memory without a restart.
  def self.send_cfg_commands(commands)
    require "socket"
    container = find_reflector_container
    return unless container

    commands.each do |cmd|
      docker_exec(container["Id"], ["sh", "-c", "printf '%s\\n' \"$1\" > /dev/shm/reflector_ctrl", "--", cmd])
    end
    Rails.logger.info "[ReflectorConfig] Sent #{commands.size} CFG command(s) to reflector"
  rescue => e
    Rails.logger.error "[ReflectorConfig] Failed to send CFG commands: #{e.message}"
  end

  # Writes a single line to the reflector's control PTY (/dev/shm/reflector_ctrl).
  # Used for runtime commands like `LOG trunk=debug`, `LOG RESET`, etc.
  # Returns true on success, false otherwise.
  def self.send_pty_command(cmd)
    require "socket"
    container = find_reflector_container
    return false unless container
    docker_exec(container["Id"], ["sh", "-c", "printf '%s\\n' \"$1\" > /dev/shm/reflector_ctrl", "--", cmd])
    Rails.logger.info "[ReflectorConfig] Sent PTY command: #{cmd}"
    true
  rescue => e
    Rails.logger.error "[ReflectorConfig] Failed to send PTY command '#{cmd}': #{e.message}"
    false
  end

  # Sends a command to the control PTY and reads back the reflector's response.
  # Opens the reader in the background BEFORE writing the command to avoid any
  # race where the reflector's response might be emitted before we start reading.
  #
  # Uses `dd bs=1` rather than `cat` because coreutils cat block-buffers stdout
  # when the consumer is a pipe, and `timeout`'s SIGTERM kills it before the
  # buffer is flushed — resulting in silent empty output. `dd bs=1` uses raw
  # write(2), so every byte is emitted as soon as it's read.
  #
  # Returns the captured output string, or nil on failure.
  def self.pty_query(command, read_timeout: 0.8, settle: 0.05)
    require "socket"
    container = find_reflector_container
    return nil unless container

    shell = <<~SH
      if [ ! -e /dev/shm/reflector_ctrl ]; then
        echo "PTY missing: /dev/shm/reflector_ctrl" >&2
        exit 1
      fi
      ( timeout #{read_timeout} dd bs=1 if=/dev/shm/reflector_ctrl 2>/dev/null ) &
      READER=$!
      sleep #{settle}
      printf '%s\\n' "$1" > /dev/shm/reflector_ctrl
      wait $READER 2>/dev/null
    SH

    out = docker_exec_capture(container["Id"], ["sh", "-c", shell, "--", command])
    Rails.logger.info "[ReflectorConfig] PTY query: #{command} (#{out.to_s.bytesize} bytes)"
    out
  rescue => e
    Rails.logger.error "[ReflectorConfig] Failed PTY query '#{command}': #{e.message}"
    nil
  end

  # Like docker_exec but captures and returns the command's stdout/stderr.
  # Parses Docker's multiplexed exec stream (8-byte frame header + payload).
  def self.docker_exec_capture(container_id, cmd)
    require "socket"
    json = { Cmd: cmd, AttachStdout: true, AttachStderr: true }.to_json
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("POST /containers/#{container_id}/exec HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: #{json.bytesize}\r\n\r\n#{json}")
    response = sock.read
    sock.close
    body = response.split("\r\n\r\n", 2).last
    exec_id = (JSON.parse(body) rescue {})["Id"]
    return nil unless exec_id

    start_body = { Detach: false, Tty: false }.to_json
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("POST /exec/#{exec_id}/start HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: #{start_body.bytesize}\r\n\r\n#{start_body}")
    response = sock.read
    sock.close
    body = response.split("\r\n\r\n", 2).last

    out = String.new
    pos = 0
    while pos + 8 <= body.bytesize
      frame_size = body.byteslice(pos + 4, 4).unpack1("N")
      break if pos + 8 + frame_size > body.bytesize
      out << body.byteslice(pos + 8, frame_size)
      pos += 8 + frame_size
    end
    (out.empty? ? body.to_s : out).force_encoding("UTF-8").scrub
  end

  def self.find_reflector_container
    require "socket"
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("GET /containers/json HTTP/1.0\r\nHost: localhost\r\n\r\n")
    response = sock.read
    sock.close
    body = response.split("\r\n\r\n", 2).last
    containers = JSON.parse(body)
    containers.find { |c| c["Names"].any? { |n| n =~ /-svxreflector-\d+$/ } }
  end

  def self.docker_exec(container_id, cmd)
    require "socket"
    json = { Cmd: cmd, AttachStdout: true, AttachStderr: true }.to_json
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("POST /containers/#{container_id}/exec HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: #{json.bytesize}\r\n\r\n#{json}")
    response = sock.read
    sock.close
    body = response.split("\r\n\r\n", 2).last
    result = JSON.parse(body) rescue {}
    exec_id = result["Id"]
    return unless exec_id

    start_body = { Detach: false, Tty: false }.to_json
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("POST /exec/#{exec_id}/start HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: #{start_body.bytesize}\r\n\r\n#{start_body}")
    sock.read
    sock.close
  end

  private

  def backup(path)
    dir = File.dirname(path)
    base = File.basename(path)
    stamp = Time.current.strftime("%Y%m%d_%H%M%S")
    FileUtils.cp(path, File.join(dir, "#{base}.#{stamp}.bak"))

    backups = Dir.glob(File.join(dir, "#{base}.*.bak")).sort
    excess = backups.size - MAX_BACKUPS
    backups.first(excess).each { |f| File.delete(f) } if excess > 0
  end
end
