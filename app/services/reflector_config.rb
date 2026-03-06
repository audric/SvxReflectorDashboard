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

  def save(path = self.class.config_path)
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
end
