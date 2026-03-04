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

      # Server cert (preserve existing)
      existing = ReflectorConfig.load
      config.server_cert = existing.server_cert

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
      Rails.logger.info "[ReflectorConfig] TG params: numbers=#{tg_numbers.inspect} allows=#{tg_allows.inspect} auto_qsy=#{tg_auto_qsys.inspect} show=#{tg_show_activities.inspect}"
      tg_numbers.each_with_index do |num, i|
        next if num.blank?
        tg_num = num.to_i
        next if tg_num <= 0

        rules = {}
        rules["ALLOW"] = tg_allows[i] if tg_allows[i].present?
        rules["AUTO_QSY_AFTER"] = tg_auto_qsys[i] if tg_auto_qsys[i].present?
        rules["SHOW_ACTIVITY"] = tg_show_activities[i] if tg_show_activities[i].present?
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
      # Find the svxreflector container via Docker socket and restart it
      require "net/http"
      socket = Net::BufferedIO.new(UNIXSocket.new("/var/run/docker.sock"))
      # List containers to find svxreflector
      request = Net::HTTP::Get.new("/containers/json")
      request.exec(socket, "1.1", "/containers/json")
      response = Net::HTTPResponse.read_new(socket)
      response.reading_body(socket, request.response_body_permitted?) {}
      containers = JSON.parse(response.body)
      container = containers.find { |c| c["Names"].any? { |n| n.include?("svxreflector") } }
      return unless container

      # Restart it
      restart_req = Net::HTTP::Post.new("/containers/#{container["Id"]}/restart")
      restart_req.exec(socket, "1.1", "/containers/#{container["Id"]}/restart")
      Net::HTTPResponse.read_new(socket).reading_body(socket, true) {}
    rescue => e
      Rails.logger.error "[ReflectorConfig] Failed to restart svxreflector: #{e.message}"
    end
  end
end
