class ReflectorConfig
  attr_accessor :global, :root_ca, :issuing_ca, :server_cert, :users, :passwords, :tg_rules

  def initialize
    @global = {}
    @root_ca = {}
    @issuing_ca = {}
    @server_cert = {}
    @users = {}       # callsign => group
    @passwords = {}   # group => password
    @tg_rules = {}    # tg_number (Integer) => { "ALLOW" => ..., "AUTO_QSY_AFTER" => ..., "SHOW_ACTIVITY" => ..., "ALLOW_MONITOR" => ... }
  end

  def self.config_path
    Rails.root.join("reflector", "svxreflector.conf")
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
      end
    end

    config.users = config.users.sort.to_h
    config.passwords = config.passwords.sort.to_h
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

    # TG rules sorted numerically
    tg_rules.keys.sort.each do |tg_num|
      lines << ""
      lines << "[TG##{tg_num}]"
      tg_rules[tg_num].each { |k, v| lines << "#{k}=#{v}" }
    end

    lines << ""
    File.write(path, lines.join("\n"))
  end

  # Syncs all users with a reflector_auth_key into the [USERS] and [PASSWORDS]
  # sections of svxreflector.conf. Adds CALLSIGN-WEB entries, removes stale ones.
  def self.sync_web_users
    config = load
    existing_web_callsigns = config.users.keys.select { |k| k.end_with?("-WEB") }

    # Build the desired state from the database
    desired = {}
    User.where.not(reflector_auth_key: [nil, ""]).where(can_monitor: true).find_each do |user|
      web_cs = "#{user.callsign.upcase}-WEB"
      desired[web_cs] = user.reflector_auth_key
    end

    changed = false

    # Add or update entries
    desired.each do |web_cs, auth_key|
      # Use the callsign (without -WEB) as the password group name
      group = web_cs
      unless config.users[web_cs] == group && config.passwords[group] == auth_key
        config.users[web_cs] = group
        config.passwords[group] = auth_key
        changed = true
      end
    end

    # Remove stale -WEB entries
    existing_web_callsigns.each do |web_cs|
      unless desired.key?(web_cs)
        config.users.delete(web_cs)
        config.passwords.delete(web_cs)
        changed = true
      end
    end

    if changed
      config.save
      Rails.logger.info "[ReflectorConfig] Synced #{desired.size} web user(s)"
    end

    changed
  end

  # Restarts the svxreflector container to pick up config changes.
  def self.restart_svxreflector
    require "socket"
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("GET /containers/json HTTP/1.0\r\nHost: localhost\r\n\r\n")
    response = sock.read
    sock.close
    body = response.split("\r\n\r\n", 2).last
    containers = JSON.parse(body)
    container = containers.find { |c| c["Names"].any? { |n| n =~ /-svxreflector-\d+$/ } }
    return unless container

    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("POST /containers/#{container["Id"]}/restart?t=5 HTTP/1.0\r\nHost: localhost\r\nContent-Length: 0\r\n\r\n")
    sock.read
    sock.close
    Rails.logger.info "[ReflectorConfig] Restarted svxreflector"
  rescue => e
    Rails.logger.error "[ReflectorConfig] Failed to restart svxreflector: #{e.message}"
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
