module Admin
  class ReflectorController < ApplicationController
    layout false
    before_action :require_reflector_admin

    def edit
      @config = ReflectorConfig.load
      @has_certs = Dir.glob(Rails.root.join("reflector_pki", "certs", "*.crt")).any?
    end

    def backups
      dir = File.dirname(ReflectorConfig.config_path)
      base = File.basename(ReflectorConfig.config_path)
      @backups = Dir.glob(File.join(dir, "#{base}.*.bak")).sort.reverse.map do |path|
        stamp = File.basename(path).match(/\.(\d{8}_\d{6})\.bak$/)&.[](1)
        label = stamp ? Time.strptime(stamp, "%Y%m%d_%H%M%S").strftime("%Y-%m-%d %H:%M:%S") : File.basename(path)
        { filename: File.basename(path), label: label, content: File.read(path) }
      end
      render json: @backups
    end

    def pending_csrs
      container = find_reflector_container
      unless container
        render json: []
        return
      end

      # Single exec: get filename, mtime, and subject for each CSR
      script = <<~SH
        for f in /var/lib/svxlink/pki/pending_csrs/*.csr; do
          [ -f "$f" ] || continue
          subj=$(openssl req -in "$f" -noout -subject 2>/dev/null | sed 's/^subject=//')
          echo "$(basename "$f" .csr)\t$(stat -c %Y "$f")\t${subj}"
        done
      SH
      output = docker_exec(container["Id"], ["sh", "-c", script])
      csrs = output.strip.split("\n").select(&:present?).map do |line|
        parts = line.split("\t", 3)
        {
          callsign: parts[0],
          date: parts[1] ? Time.at(parts[1].to_i).strftime("%Y-%m-%d %H:%M") : nil,
          subject: parts[2].to_s.strip.presence
        }
      end
      render json: csrs
    rescue => e
      Rails.logger.error "[ReflectorConfig] Failed to list pending CSRs: #{e.class} #{e.message}"
      render json: []
    end

    def inspect_csr
      callsign = params[:callsign].to_s.strip.gsub(/[^A-Za-z0-9\-_]/, "")
      container = find_reflector_container
      unless container
        render json: { error: "Reflector container not found" }, status: :not_found
        return
      end

      output = docker_exec(container["Id"], [
        "openssl", "req", "-in", "/var/lib/svxlink/pki/pending_csrs/#{callsign}.csr", "-noout",
        "-subject", "-nameopt", "multiline"
      ])
      key_info = docker_exec(container["Id"], [
        "openssl", "req", "-in", "/var/lib/svxlink/pki/pending_csrs/#{callsign}.csr", "-noout", "-text"
      ])
      # Extract just the public key info section
      key_lines = []
      in_key = false
      key_info.each_line do |line|
        if line =~ /Public Key Algorithm|Public-Key/
          in_key = true
        elsif in_key && line =~ /\A\s{8,}/
          # continuation of key block
        elsif in_key
          in_key = false
        end
        key_lines << line.rstrip if in_key
      end

      render json: {
        callsign: callsign,
        subject: output.strip,
        key_info: key_lines.join("\n")
      }
    rescue => e
      Rails.logger.error "[ReflectorConfig] Failed to inspect CSR: #{e.class} #{e.message}"
      render json: { error: e.message }, status: :internal_server_error
    end

    def sign_csr
      callsign = params[:callsign].to_s.strip
      if callsign.blank?
        render json: { error: "Callsign required" }, status: :bad_request
        return
      end

      container = find_reflector_container
      unless container
        render json: { error: "Reflector container not found" }, status: :not_found
        return
      end

      safe_callsign = callsign.gsub(/[^A-Za-z0-9\-_]/, "")
      docker_exec(container["Id"], ["sh", "-c", "printf '%s\\n' \"$1\" > /dev/shm/reflector_ctrl", "--", "CA SIGN #{safe_callsign}"])
      render json: { ok: true, callsign: safe_callsign }
    rescue => e
      Rails.logger.error "[ReflectorConfig] Failed to sign CSR: #{e.class} #{e.message}"
      render json: { error: e.message }, status: :internal_server_error
    end

    def reject_csr
      callsign = params[:callsign].to_s.strip.gsub(/[^A-Za-z0-9\-_]/, "")
      if callsign.blank?
        render json: { error: "Callsign required" }, status: :bad_request
        return
      end

      container = find_reflector_container
      unless container
        render json: { error: "Reflector container not found" }, status: :not_found
        return
      end

      docker_exec(container["Id"], ["rm", "-f", "/var/lib/svxlink/pki/pending_csrs/#{callsign}.csr"])
      render json: { ok: true, callsign: callsign }
    rescue => e
      Rails.logger.error "[ReflectorConfig] Failed to reject CSR: #{e.class} #{e.message}"
      render json: { error: e.message }, status: :internal_server_error
    end

    def certificates
      container = find_reflector_container
      unless container
        render json: { error: "Reflector container not found" }, status: :not_found
        return
      end

      script = <<~SH
        for name in svxreflector_root_ca svxreflector_issuing_ca; do
          f="/var/lib/svxlink/pki/certs/${name}.crt"
          [ -f "$f" ] && echo "CERT:${name}" && openssl x509 -in "$f" -noout -subject -issuer -dates -serial -ext subjectAltName 2>/dev/null && echo "---"
        done
        for f in /var/lib/svxlink/pki/certs/*.crt; do
          bn=$(basename "$f" .crt)
          [ "$bn" = "svxreflector_root_ca" ] && continue
          [ "$bn" = "svxreflector_issuing_ca" ] && continue
          echo "CERT:${bn}" && openssl x509 -in "$f" -noout -subject -issuer -dates -serial -ext subjectAltName 2>/dev/null && echo "---"
        done
      SH
      output = docker_exec(container["Id"], ["sh", "-c", script])

      certs = []
      current = nil
      output.each_line do |line|
        line = line.strip
        if line.start_with?("CERT:")
          current = { name: line.sub("CERT:", ""), fields: {} }
          certs << current
        elsif line == "---"
          current = nil
        elsif current && line.include?("=")
          key, val = line.split("=", 2)
          current[:fields][key.strip] = val.to_s.strip
        elsif current && line.present?
          # Multi-line field continuation (e.g. SAN)
          last_key = current[:fields].keys.last
          current[:fields][last_key] = "#{current[:fields][last_key]} #{line}".strip if last_key
        end
      end

      render json: certs
    rescue => e
      Rails.logger.error "[ReflectorConfig] Failed to list certificates: #{e.class} #{e.message}"
      render json: []
    end

    def export_ca_bundle
      container = find_reflector_container
      unless container
        head :not_found
        return
      end

      bundle = docker_exec(container["Id"], ["cat", "/var/lib/svxlink/pki/ca-bundle.crt"])
      send_data bundle, filename: "ca-bundle.crt", type: "application/x-pem-file", disposition: "attachment"
    rescue => e
      Rails.logger.error "[ReflectorConfig] Failed to export CA bundle: #{e.class} #{e.message}"
      head :internal_server_error
    end

    def block_node
      callsign = params[:callsign].to_s.strip.gsub(/[^A-Za-z0-9\-_]/, "")
      seconds = params[:seconds].to_i
      if callsign.blank?
        render json: { error: "Callsign required" }, status: :bad_request
        return
      end

      container = find_reflector_container
      unless container
        render json: { error: "Reflector container not found" }, status: :not_found
        return
      end

      docker_exec(container["Id"], ["sh", "-c", "printf '%s\\n' \"$1\" > /dev/shm/reflector_ctrl", "--", "NODE BLOCK #{callsign} #{seconds}"])
      render json: { ok: true, callsign: callsign, seconds: seconds }
    rescue => e
      Rails.logger.error "[ReflectorConfig] Failed to block node: #{e.class} #{e.message}"
      render json: { error: e.message }, status: :internal_server_error
    end

    def revoke_cert
      callsign = params[:callsign].to_s.strip.gsub(/[^A-Za-z0-9\-_]/, "")
      if callsign.blank?
        render json: { error: "Callsign required" }, status: :bad_request
        return
      end

      container = find_reflector_container
      unless container
        render json: { error: "Reflector container not found" }, status: :not_found
        return
      end

      docker_exec(container["Id"], ["sh", "-c", "printf '%s\\n' \"$1\" > /dev/shm/reflector_ctrl", "--", "CA RM #{callsign}"])
      render json: { ok: true, callsign: callsign }
    rescue => e
      Rails.logger.error "[ReflectorConfig] Failed to revoke certificate: #{e.class} #{e.message}"
      render json: { error: e.message }, status: :internal_server_error
    end

    def reset_pki
      container = find_reflector_container
      unless container
        render json: { error: "Reflector container not found" }, status: :not_found
        return
      end

      docker_exec(container["Id"], ["sh", "-c", "rm -rf /var/lib/svxlink/pki/*"])
      docker_api_post("/containers/#{container["Id"]}/restart?t=5")
      render json: { ok: true }
    rescue => e
      Rails.logger.error "[ReflectorConfig] Failed to reset PKI: #{e.class} #{e.message}"
      render json: { error: e.message }, status: :internal_server_error
    end

    def update
      config = ReflectorConfig.new
      existing = ReflectorConfig.load

      # Trunk mode: reflector or satellite — strip the opposite mode's keys
      trunk_mode = params.dig(:config, :trunk_mode).to_s
      satellite_keys = %w[SATELLITE_OF SATELLITE_PORT SATELLITE_SECRET SATELLITE_ID]
      reflector_only_keys = %w[LOCAL_PREFIX CLUSTER_TGS]

      # Global settings
      (params.dig(:config, :global) || {}).each do |key, value|
        # In reflector mode, skip satellite global keys
        next if trunk_mode == 'reflector' && satellite_keys.include?(key)
        # In satellite mode, skip trunk/cluster global keys
        next if trunk_mode == 'satellite' && reflector_only_keys.include?(key)
        config.global[key] = value if value.present?
      end

      # Ensure COMMAND_PTY is always set — required for node block, PKI signing, etc.
      config.global["COMMAND_PTY"] ||= "/dev/shm/reflector_ctrl"

      # Certificate sections (ROOT_CA, ISSUING_CA, SERVER_CERT)
      # When certs exist, these fields are disabled in the form and not submitted.
      # Preserve existing values so they aren't silently dropped from the config file.
      %w[root_ca issuing_ca server_cert].each do |section|
        submitted = params.dig(:config, section.to_sym)
        if submitted.present?
          submitted.each do |key, value|
            config.send(section)[key] = value if value.present?
          end
        else
          config.send("#{section}=", existing.send(section))
        end
      end

      # Users
      callsigns = Array(params.dig(:config, :user_callsigns))
      groups = Array(params.dig(:config, :user_groups))
      callsigns.zip(groups).each do |callsign, group|
        config.users[callsign.strip] = group.strip if callsign.present? && group.present?
      end

      # Passwords
      group_names = Array(params.dig(:config, :password_names))
      group_passwords = Array(params.dig(:config, :password_values))
      group_names.zip(group_passwords).each do |name, password|
        config.passwords[name.strip] = password.strip if name.present? && password.present?
      end

      # TG rules
      cfg = params[:config] || {}
      tg_numbers = Array(cfg[:tg_numbers]).map(&:to_s)
      tg_allows = Array(cfg[:tg_allows]).map(&:to_s)
      tg_auto_qsys = Array(cfg[:tg_auto_qsys]).map(&:to_s)
      tg_show_activities = Array(cfg[:tg_show_activities]).map(&:to_s)
      tg_allow_monitors = Array(cfg[:tg_allow_monitors]).map(&:to_s)
      tg_numbers.each_with_index do |num, i|
        next if num.blank?
        tg_num = num.to_i
        next if tg_num <= 0

        rules = {}
        rules["ALLOW"] = tg_allows[i] if tg_allows[i].present?
        rules["AUTO_QSY_AFTER"] = tg_auto_qsys[i] if tg_auto_qsys[i].present?
        rules["SHOW_ACTIVITY"] = tg_show_activities[i] if tg_show_activities[i].present?
        rules["ALLOW_MONITOR"] = tg_allow_monitors[i] if tg_allow_monitors[i].present?
        config.tg_rules[tg_num] = rules
      end

      # Trunk peers (reflector mode only)
      if trunk_mode != 'satellite'
        trunk_names = Array(cfg[:trunk_names])
        trunk_hosts = Array(cfg[:trunk_hosts])
        trunk_ports = Array(cfg[:trunk_ports])
        trunk_secrets = Array(cfg[:trunk_secrets])
        trunk_prefixes = Array(cfg[:trunk_remote_prefixes])
        trunk_config_urls = Array(cfg[:trunk_config_urls])
        trunk_status_urls = Array(cfg[:trunk_status_urls])
        trunk_names.each_with_index do |name, i|
          next if name.blank?
          config.trunks[name.strip] = {
            "HOST" => trunk_hosts[i].to_s.strip,
            "PORT" => trunk_ports[i].to_s.strip,
            "SECRET" => trunk_secrets[i].to_s.strip,
            "REMOTE_PREFIX" => trunk_prefixes[i].to_s.strip,
            "CONFIG_URL" => trunk_config_urls[i].to_s.strip,
            "STATUS_URL" => trunk_status_urls[i].to_s.strip
          }.reject { |_, v| v.blank? }
        end

        # Satellite server section (reflector mode only, requires both port and secret)
        sat_port = cfg.dig(:satellite, :LISTEN_PORT).to_s.strip
        sat_secret = cfg.dig(:satellite, :SECRET).to_s.strip
        if sat_port.present? && sat_secret.present?
          config.satellite["LISTEN_PORT"] = sat_port
          config.satellite["SECRET"] = sat_secret
        end
      end

      config.save
      restart_svxreflector
      refresh_reflector_config_cache
      redirect_to edit_admin_reflector_path, notice: "Configuration saved and svxreflector restarted."
    end
    private

    def require_reflector_admin
      require_admin
      return if performed?
      unless current_user.reflector_admin?
        redirect_to root_path, alert: "Reflector admin access required"
      end
    end

    # After saving config and restarting the reflector, immediately refresh
    # the cached /config so the UI reflects the new mode without waiting for
    # the updater's 60-second poll cycle.
    def refresh_reflector_config_cache
      status_url = Setting.get('reflector_status_url', ENV.fetch('REFLECTOR_STATUS_URL', 'http://svxreflector:8080/status'))
      uri = URI.parse(status_url)
      uri.path = '/config'
      # Give the reflector a moment to restart before fetching
      Thread.new do
        sleep 6
        begin
          res = Net::HTTP.get_response(uri)
          if res.is_a?(Net::HTTPSuccess)
            redis = Redis.new(url: ENV.fetch('REDIS_URL', 'redis://localhost:6379/1'))
            redis.set('reflector:config', res.body)
            Rails.logger.info "[ReflectorConfig] Refreshed /config cache after save"
          end
        rescue => e
          Rails.logger.warn "[ReflectorConfig] /config cache refresh failed: #{e.message}"
        end
      end
    end

    def restart_svxreflector
      require "net/http"
      require "socket"

      # List containers to find svxreflector
      containers = docker_api_get("/containers/json")
      container = containers.find { |c| c["Names"].any? { |n| n =~ /-svxreflector-\d+$/ } }
      unless container
        Rails.logger.error "[ReflectorConfig] svxreflector container not found"
        return
      end

      Rails.logger.info "[ReflectorConfig] Restarting container #{container["Id"][0..11]} (#{container["Names"].first})"
      docker_api_post("/containers/#{container["Id"]}/restart?t=5")
      Rails.logger.info "[ReflectorConfig] Restart request sent"
    rescue => e
      Rails.logger.error "[ReflectorConfig] Failed to restart svxreflector: #{e.class} #{e.message}"
    end

    def docker_api_get(path)
      sock = UNIXSocket.new("/var/run/docker.sock")
      sock.write("GET #{path} HTTP/1.0\r\nHost: localhost\r\n\r\n")
      response = sock.read
      sock.close
      body = response.split("\r\n\r\n", 2).last
      JSON.parse(body)
    end

    def docker_api_post(path)
      sock = UNIXSocket.new("/var/run/docker.sock")
      sock.write("POST #{path} HTTP/1.0\r\nHost: localhost\r\nContent-Length: 0\r\n\r\n")
      response = sock.read
      sock.close
      Rails.logger.info "[ReflectorConfig] Docker API response: #{response.split("\r\n").first}"
    end

    def docker_api_post_json(path, data)
      json = data.to_json
      sock = UNIXSocket.new("/var/run/docker.sock")
      sock.write("POST #{path} HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: #{json.bytesize}\r\n\r\n#{json}")
      response = sock.read
      sock.close
      body = response.split("\r\n\r\n", 2).last
      JSON.parse(body) rescue {}
    end

    def find_reflector_container
      containers = docker_api_get("/containers/json")
      containers.find { |c| c["Names"].any? { |n| n =~ /-svxreflector-\d+$/ } }
    end

    def docker_exec(container_id, cmd)
      result = docker_api_post_json("/containers/#{container_id}/exec", {
        Cmd: cmd, AttachStdout: true, AttachStderr: true
      })
      exec_id = result["Id"]
      return "" unless exec_id

      start_body = { Detach: false, Tty: false }.to_json
      sock = UNIXSocket.new("/var/run/docker.sock")
      sock.write("POST /exec/#{exec_id}/start HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: #{start_body.bytesize}\r\n\r\n#{start_body}")
      response = sock.read
      sock.close

      raw = response.split("\r\n\r\n", 2).last
      parse_exec_output(raw)
    end

    def parse_exec_output(raw)
      output = ""
      pos = 0
      while pos + 8 <= raw.bytesize
        size = raw[pos + 4, 4].unpack1("N")
        break if pos + 8 + size > raw.bytesize
        output << raw[pos + 8, size]
        pos += 8 + size
      end
      output
    end
  end
end
