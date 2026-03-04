module Admin
  class ReflectorController < ApplicationController
    layout false
    before_action :require_reflector_admin

    def edit
      @config = ReflectorConfig.load
    end

    def update
      config = ReflectorConfig.new

      # Global settings
      (params.dig(:config, :global) || {}).each do |key, value|
        config.global[key] = value if value.present?
      end

      # Certificate sections (ROOT_CA, ISSUING_CA, SERVER_CERT)
      %w[root_ca issuing_ca server_cert].each do |section|
        (params.dig(:config, section.to_sym) || {}).each do |key, value|
          config.send(section)[key] = value if value.present?
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

      config.save
      restart_svxreflector
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
  end
end
