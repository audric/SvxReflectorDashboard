module Admin
  class BridgeController < ApplicationController
    layout false
    before_action :require_reflector_admin
    before_action :set_bridge, only: %i[edit update destroy toggle backups logs]

    # Called from update.sh to restart all enabled bridges after image pull
    def self.restart_enabled_bridges
      ctrl = new
      Bridge.where(enabled: true).find_each do |bridge|
        begin
          puts "  Starting #{bridge.name}..."
          bridge.generate_config
          ctrl.send(:start_or_recreate_container, bridge)
        rescue => e
          puts "  Failed #{bridge.name}: #{e.message}"
        end
      end
    end

    def index
      @bridges = Bridge.includes(:bridge_tg_mappings).order(:name)
      @container_statuses = fetch_container_statuses
      @container_last_lines = {}
      @bridges.each do |bridge|
        next unless bridge.enabled? && @container_statuses[bridge.id]
        lines = fetch_container_logs(bridge, tail: 5)
        @container_last_lines[bridge.id] = lines if lines.present?
      end
    end

    def new
      bridge_type = params[:type].presence || "reflector"
      defaults = {
        bridge_type: bridge_type,
        local_host: "svxreflector",
        local_port: 5300,
        local_default_tg: 1
      }
      if bridge_type == "echolink"
        defaults.merge!(
          echolink_max_qsos: 10,
          echolink_max_connections: 11,
          echolink_link_idle_timeout: 300,
          echolink_servers: "servers.echolink.org",
          echolink_proxy_port: 8100,
          echolink_proxy_password: "PUBLIC"
        )
      elsif bridge_type == "xlx"
        defaults.merge!(
          xlx_port: 30051,
          xlx_module: "A",
          xlx_protocol: "DCS"
        )
      elsif bridge_type == "dmr"
        defaults.merge!(
          dmr_port: 62030,
          dmr_timeslot: 2,
          dmr_color_code: 1
        )
      elsif bridge_type == "ysf"
        defaults.merge!(
          ysf_port: 42000
        )
      elsif bridge_type == "allstar"
        defaults.merge!(
          allstar_port: 4569
        )
      elsif bridge_type == "iax"
        defaults.merge!(
          iax_port: 4569,
          iax_context: "friend",
          iax_mode: "persistent",
          iax_idle_timeout: 30,
          iax_codecs: "gsm,ulaw,alaw,g726"
        )
      elsif bridge_type == "sip"
        defaults.merge!(
          sip_port: 5060,
          sip_transport: "udp",
          sip_mode: "persistent",
          sip_idle_timeout: 30,
          sip_codecs: "opus,g722,gsm,ulaw,alaw",
          sip_dtmf_delay: 2000,
          sip_log_level: 1
        )
      else
        defaults.merge!(
          remote_port: 5300,
          remote_default_tg: 1,
          timeout: 0
        )
      end
      @bridge = Bridge.new(defaults)
    end

    def create
      @bridge = Bridge.new(bridge_params)
      if @bridge.save
        save_tg_mappings(@bridge) if @bridge.reflector?
        @bridge.generate_config
        redirect_to admin_bridges_path, notice: "Bridge \"#{@bridge.name}\" created."
      else
        render :new, status: :unprocessable_entity
      end
    end

    def edit; end

    def update
      name_changed = bridge_params[:name] != @bridge.name
      if @bridge.update(bridge_params)
        save_tg_mappings(@bridge) if @bridge.reflector?
        @bridge.generate_config
        if @bridge.enabled?
          if name_changed
            recreate_container(@bridge)
          else
            restart_container(@bridge)
          end
        end
        redirect_to admin_bridges_path, notice: "Bridge \"#{@bridge.name}\" updated."
      else
        render :edit, status: :unprocessable_entity
      end
    end

    def destroy
      stop_container(@bridge)
      remove_container(@bridge)
      @bridge.destroy
      redirect_to admin_bridges_path, notice: "Bridge deleted."
    end

    def backups
      dir = @bridge.backups_dir
      snapshots = Dir.glob(dir.join("*")).select { |d| File.directory?(d) }.sort.reverse.map do |snap_dir|
        stamp = File.basename(snap_dir)
        time_label = Time.strptime(stamp, "%Y%m%d_%H%M%S").strftime("%Y-%m-%d %H:%M:%S") rescue stamp
        files = Dir.glob(File.join(snap_dir, "*")).sort.map do |f|
          { name: File.basename(f), content: File.read(f) }
        end
        { label: time_label, files: files }
      end
      render json: snapshots
    end

    def logs
      logs = fetch_container_logs(@bridge, tail: params.fetch(:tail, 50).to_i)
      render json: { logs: logs || "No logs available (container not found)" }
    end

    def xlx_hosts
      protocol = params[:protocol].to_s.upcase
      url = case protocol
            when "DEXTRA" then "https://www.pistar.uk/downloads/DExtra_Hosts.txt"
            else "https://www.pistar.uk/downloads/DCS_Hosts.txt"
            end
      Rails.cache.delete("xlx_hosts:#{protocol}") if params[:refresh] == "1"
      body = Rails.cache.fetch("xlx_hosts:#{protocol}", expires_in: 1.day) do
        require "net/http"
        uri = URI(url)
        Net::HTTP.get(uri) rescue ""
      end
      hosts = body.lines.filter_map do |line|
        next if line.start_with?("#") || line.strip.empty?
        parts = line.strip.split("\t")
        next unless parts.length >= 2
        { name: parts[0], host: parts[1] }
      end
      render json: hosts
    end

    def toggle
      if @bridge.enabled?
        stop_container(@bridge)
        @bridge.update(enabled: false)
        redirect_to admin_bridges_path, notice: "Bridge \"#{@bridge.name}\" stopped."
      else
        @bridge.update(enabled: true)
        start_or_recreate_container(@bridge)
        redirect_to admin_bridges_path, notice: "Bridge \"#{@bridge.name}\" started."
      end
    end

    private

    def set_bridge
      @bridge = Bridge.find(params[:id])
    end

    def bridge_params
      params.require(:bridge).permit(
        :name, :bridge_type, :local_host, :local_port, :local_callsign, :local_auth_key,
        :local_default_tg, :remote_host, :remote_port, :remote_callsign,
        :remote_auth_key, :remote_default_tg, :timeout, :enabled,
        :remote_ca_bundle, :node_location, :sysop,
        :jitter_buffer_delay, :monitor_tgs, :tg_select_timeout,
        :mute_first_tx_loc, :mute_first_tx_rem, :verbose,
        :udp_heartbeat_interval,
        :cert_subj_c, :cert_subj_o, :cert_subj_ou, :cert_subj_l,
        :cert_subj_st, :cert_subj_gn, :cert_subj_sn, :cert_email,
        :echolink_callsign, :echolink_password, :echolink_sysopname,
        :echolink_location, :echolink_description,
        :echolink_max_qsos, :echolink_max_connections, :echolink_link_idle_timeout,
        :echolink_proxy_server, :echolink_proxy_port, :echolink_proxy_password,
        :echolink_autocon_echolink_id, :echolink_autocon_time,
        :echolink_accept_incoming, :echolink_reject_incoming, :echolink_drop_incoming,
        :echolink_accept_outgoing, :echolink_reject_outgoing,
        :echolink_reject_conf, :echolink_use_gsm_only, :echolink_bind_addr,
        :echolink_servers, :default_active,
        :xlx_host, :xlx_port, :xlx_module, :xlx_callsign, :xlx_callsign_suffix, :xlx_mycall, :xlx_mycall_suffix, :xlx_reflector_name, :xlx_protocol,
        :dmr_host, :dmr_port, :dmr_id, :dmr_password, :dmr_talkgroup, :dmr_timeslot, :dmr_color_code, :dmr_callsign,
        :ysf_host, :ysf_port, :ysf_callsign, :ysf_description,
        :allstar_node, :allstar_password, :allstar_server, :allstar_port,
        :iax_username, :iax_password, :iax_server, :iax_port,
        :iax_extension, :iax_context, :iax_mode, :iax_idle_timeout, :iax_codecs,
        :sip_username, :sip_password, :sip_server, :sip_port,
        :sip_extension, :sip_transport, :sip_mode, :sip_idle_timeout, :sip_codecs,
        :sip_dtmf, :sip_dtmf_delay, :sip_caller_id, :sip_log_level, :sip_pin, :sip_pin_timeout, :sip_vox_timeout, :sip_ptt_key, :sip_max_call_duration,
        :zello_username, :zello_password, :zello_channel, :zello_channel_password, :zello_issuer_id, :zello_private_key,
        :agc_target_level, :agc_attack_rate, :agc_decay_rate, :agc_max_gain, :agc_min_gain, :agc_limit_level,
        :filter_hpf_cutoff, :filter_lpf_cutoff
      )
    end

    def save_tg_mappings(bridge)
      bridge.bridge_tg_mappings.destroy_all
      local_tgs = Array(params.dig(:bridge, :mapping_local_tgs))
      remote_tgs = Array(params.dig(:bridge, :mapping_remote_tgs))
      timeouts = Array(params.dig(:bridge, :mapping_timeouts))
      default_actives = Array(params.dig(:bridge, :mapping_default_actives))
      local_tgs.zip(remote_tgs, timeouts, default_actives).each do |local_tg, remote_tg, tout, active|
        next if local_tg.blank? || remote_tg.blank?
        bridge.bridge_tg_mappings.create(
          local_tg: local_tg.to_i, remote_tg: remote_tg.to_i,
          timeout: tout.to_i, default_active: active == "1"
        )
      end
    end

    def require_reflector_admin
      require_admin
      return if performed?
      unless current_user.reflector_admin?
        redirect_to root_path, alert: "Reflector admin access required"
      end
    end

    # ── Docker container management ──

    def docker_network
      # Find the network used by the svxreflector container
      containers = docker_api_get("/containers/json")
      reflector = containers.find { |c| c["Names"].any? { |n| n =~ /svxreflector/ && n !~ /bridge/ } }
      return nil unless reflector
      networks = reflector.dig("NetworkSettings", "Networks") || {}
      networks.keys.first
    end

    def start_or_recreate_container(bridge)
      existing = find_container(bridge, all: true)
      if existing
        stop_container(bridge) if existing["State"] == "running"
        remove_container(bridge)
      end

      if bridge.xlx?
        pull_image("ghcr.io/audric/svxreflectordashboard-xlx-bridge")
        start_xlx_container(bridge)
      elsif bridge.dmr?
        pull_image("ghcr.io/audric/svxreflectordashboard-dmr-bridge")
        start_dmr_container(bridge)
      elsif bridge.ysf?
        pull_image("ghcr.io/audric/svxreflectordashboard-ysf-bridge")
        start_ysf_container(bridge)
      elsif bridge.allstar?
        pull_image("ghcr.io/audric/svxreflectordashboard-allstar-bridge")
        start_allstar_container(bridge)
      elsif bridge.iax?
        pull_image("ghcr.io/audric/svxreflectordashboard-iax-bridge") rescue nil
        start_iax_container(bridge)
      elsif bridge.sip?
        pull_image("ghcr.io/audric/svxreflectordashboard-sip-bridge") rescue nil
        start_sip_container(bridge)
      elsif bridge.zello?
        pull_image("ghcr.io/audric/svxreflectordashboard-zello-bridge") rescue nil
        start_zello_container(bridge)
      else
        pull_image("ghcr.io/audric/svxlink-docker")
        start_svxlink_container(bridge)
      end
    rescue => e
      Rails.logger.error "[Bridge] Failed to start container: #{e.class} #{e.message}"
    end

    def start_xlx_container(bridge)
      network = docker_network
      body = {
        Image: "ghcr.io/audric/svxreflectordashboard-xlx-bridge",
        Labels: {
          "svx.bridge" => "true",
          "svx.bridge.id" => bridge.id.to_s,
          "svx.bridge.name" => bridge.name,
          "com.docker.compose.project" => "",
          "com.docker.compose.service" => ""
        },
        Env: [
          "REFLECTOR_HOST=#{bridge.local_host}",
          "REFLECTOR_PORT=#{bridge.local_port}",
          "REFLECTOR_AUTH_KEY=#{bridge.local_auth_key}",
          "REFLECTOR_TG=#{bridge.local_default_tg}",
          "XLX_HOST=#{bridge.xlx_host}",
          "XLX_PORT=#{bridge.xlx_port || (bridge.xlx_protocol == 'DEXTRA' ? 30001 : 30051)}",
          "XLX_MODULE=#{bridge.xlx_module}",
          "XLX_PROTOCOL=#{bridge.xlx_protocol.presence || 'DCS'}",
          "XLX_REFLECTOR_NAME=#{bridge.xlx_reflector_name.presence || 'XLX000'}",
          "CALLSIGN=#{bridge.local_callsign}",
          "XLX_CALLSIGN=#{bridge.dcs_callsign}",
          "XLX_MYCALL=#{bridge.xlx_mycall}",
          "XLX_MYCALL_SUFFIX=#{bridge.xlx_mycall_suffix.presence || 'AMBE'}",
          "NODE_LOCATION=#{bridge.node_location.presence || bridge.name}",
          "SYSOP=#{bridge.sysop}",
          "REDIS_URL=#{ENV.fetch('REDIS_URL', 'redis://redis:6379/1')}"
        ] + agc_env_array(bridge),
        HostConfig: {
          RestartPolicy: { Name: "unless-stopped" }
        }
      }
      body[:NetworkingConfig] = { EndpointsConfig: { network => {} } } if network

      result = docker_api_post_json("/containers/create?name=#{bridge.container_name}", body)
      if result && result["Id"]
        docker_api_post("/containers/#{result["Id"]}/start")
        Rails.logger.info "[Bridge] Created and started XLX container #{bridge.container_name}"
      end
    end

    def start_dmr_container(bridge)
      network = docker_network
      body = {
        Image: "ghcr.io/audric/svxreflectordashboard-dmr-bridge",
        Labels: {
          "svx.bridge" => "true",
          "svx.bridge.id" => bridge.id.to_s,
          "svx.bridge.name" => bridge.name,
          "com.docker.compose.project" => "",
          "com.docker.compose.service" => ""
        },
        Env: [
          "REFLECTOR_HOST=#{bridge.local_host}",
          "REFLECTOR_PORT=#{bridge.local_port}",
          "REFLECTOR_AUTH_KEY=#{bridge.local_auth_key}",
          "REFLECTOR_TG=#{bridge.local_default_tg}",
          "CALLSIGN=#{bridge.local_callsign}",
          "DMR_HOST=#{bridge.dmr_host}",
          "DMR_PORT=#{bridge.dmr_port || 62030}",
          "DMR_ID=#{bridge.dmr_id}",
          "DMR_PASSWORD=#{bridge.dmr_password}",
          "DMR_TALKGROUP=#{bridge.dmr_talkgroup}",
          "DMR_TIMESLOT=#{bridge.dmr_timeslot || 2}",
          "DMR_COLOR_CODE=#{bridge.dmr_color_code || 1}",
          "DMR_CALLSIGN=#{bridge.dmr_callsign.presence || bridge.local_callsign}",
          "NODE_LOCATION=#{bridge.node_location.presence || bridge.name}",
          "SYSOP=#{bridge.sysop}",
          "REDIS_URL=#{ENV.fetch('REDIS_URL', 'redis://redis:6379/1')}"
        ] + agc_env_array(bridge),
        HostConfig: {
          RestartPolicy: { Name: "unless-stopped" }
        }
      }
      body[:NetworkingConfig] = { EndpointsConfig: { network => {} } } if network

      result = docker_api_post_json("/containers/create?name=#{bridge.container_name}", body)
      if result && result["Id"]
        docker_api_post("/containers/#{result["Id"]}/start")
        Rails.logger.info "[Bridge] Created and started DMR container #{bridge.container_name}"
      end
    end

    def start_ysf_container(bridge)
      network = docker_network
      body = {
        Image: "ghcr.io/audric/svxreflectordashboard-ysf-bridge",
        Labels: {
          "svx.bridge" => "true",
          "svx.bridge.id" => bridge.id.to_s,
          "svx.bridge.name" => bridge.name,
          "com.docker.compose.project" => "",
          "com.docker.compose.service" => ""
        },
        Env: [
          "REFLECTOR_HOST=#{bridge.local_host}",
          "REFLECTOR_PORT=#{bridge.local_port}",
          "REFLECTOR_AUTH_KEY=#{bridge.local_auth_key}",
          "REFLECTOR_TG=#{bridge.local_default_tg}",
          "CALLSIGN=#{bridge.local_callsign}",
          "YSF_HOST=#{bridge.ysf_host}",
          "YSF_PORT=#{bridge.ysf_port || 42000}",
          "YSF_CALLSIGN=#{bridge.ysf_callsign.presence || bridge.local_callsign}",
          "YSF_DESCRIPTION=#{bridge.ysf_description}",
          "NODE_LOCATION=#{bridge.node_location.presence || bridge.name}",
          "SYSOP=#{bridge.sysop}",
          "REDIS_URL=#{ENV.fetch('REDIS_URL', 'redis://redis:6379/1')}"
        ] + agc_env_array(bridge),
        HostConfig: {
          RestartPolicy: { Name: "unless-stopped" }
        }
      }
      body[:NetworkingConfig] = { EndpointsConfig: { network => {} } } if network

      result = docker_api_post_json("/containers/create?name=#{bridge.container_name}", body)
      if result && result["Id"]
        docker_api_post("/containers/#{result["Id"]}/start")
        Rails.logger.info "[Bridge] Created and started YSF container #{bridge.container_name}"
      end
    end

    def start_allstar_container(bridge)
      network = docker_network
      body = {
        Image: "ghcr.io/audric/svxreflectordashboard-allstar-bridge",
        Labels: {
          "svx.bridge" => "true",
          "svx.bridge.id" => bridge.id.to_s,
          "svx.bridge.name" => bridge.name,
          "com.docker.compose.project" => "",
          "com.docker.compose.service" => ""
        },
        Env: [
          "REFLECTOR_HOST=#{bridge.local_host}",
          "REFLECTOR_PORT=#{bridge.local_port}",
          "REFLECTOR_AUTH_KEY=#{bridge.local_auth_key}",
          "REFLECTOR_TG=#{bridge.local_default_tg}",
          "CALLSIGN=#{bridge.local_callsign}",
          "ALLSTAR_NODE=#{bridge.allstar_node}",
          "ALLSTAR_PASSWORD=#{bridge.allstar_password}",
          "ALLSTAR_SERVER=#{bridge.allstar_server}",
          "ALLSTAR_PORT=#{bridge.allstar_port || 4569}",
          "NODE_LOCATION=#{bridge.node_location.presence || bridge.name}",
          "SYSOP=#{bridge.sysop}",
          "REDIS_URL=#{ENV.fetch('REDIS_URL', 'redis://redis:6379/1')}"
        ] + agc_env_array(bridge),
        HostConfig: {
          RestartPolicy: { Name: "unless-stopped" }
        }
      }
      body[:NetworkingConfig] = { EndpointsConfig: { network => {} } } if network

      result = docker_api_post_json("/containers/create?name=#{bridge.container_name}", body)
      if result && result["Id"]
        docker_api_post("/containers/#{result["Id"]}/start")
        Rails.logger.info "[Bridge] Created and started AllStar container #{bridge.container_name}"
      end
    end

    def start_iax_container(bridge)
      network = docker_network
      body = {
        Image: "ghcr.io/audric/svxreflectordashboard-iax-bridge",
        Labels: {
          "svx.bridge" => "true",
          "svx.bridge.id" => bridge.id.to_s,
          "svx.bridge.name" => bridge.name,
          "com.docker.compose.project" => "",
          "com.docker.compose.service" => ""
        },
        Env: [
          "REFLECTOR_HOST=#{bridge.local_host}",
          "REFLECTOR_PORT=#{bridge.local_port}",
          "REFLECTOR_AUTH_KEY=#{bridge.local_auth_key}",
          "REFLECTOR_TG=#{bridge.local_default_tg}",
          "CALLSIGN=#{bridge.local_callsign}",
          "IAX_USERNAME=#{bridge.iax_username}",
          "IAX_PASSWORD=#{bridge.iax_password}",
          "IAX_SERVER=#{bridge.iax_server}",
          "IAX_PORT=#{bridge.iax_port || 4569}",
          "IAX_EXTENSION=#{bridge.iax_extension}",
          "IAX_CONTEXT=#{bridge.iax_context.presence || 'friend'}",
          "IAX_MODE=#{bridge.iax_mode.presence || 'persistent'}",
          "IAX_IDLE_TIMEOUT=#{bridge.iax_idle_timeout || 30}",
          "IAX_CODECS=#{bridge.iax_codecs.presence || 'gsm,ulaw,alaw,g726'}",
          "NODE_LOCATION=#{bridge.node_location.presence || bridge.name}",
          "SYSOP=#{bridge.sysop}",
          "REDIS_URL=#{ENV.fetch('REDIS_URL', 'redis://redis:6379/1')}"
        ] + agc_env_array(bridge),
        HostConfig: {
          RestartPolicy: { Name: "unless-stopped" }
        }
      }
      body[:NetworkingConfig] = { EndpointsConfig: { network => {} } } if network

      result = docker_api_post_json("/containers/create?name=#{bridge.container_name}", body)
      if result && result["Id"]
        docker_api_post("/containers/#{result["Id"]}/start")
        Rails.logger.info "[Bridge] Created and started IAX container #{bridge.container_name}"
      end
    end

    def start_sip_container(bridge)
      network = docker_network
      body = {
        Image: "ghcr.io/audric/svxreflectordashboard-sip-bridge",
        Labels: {
          "svx.bridge" => "true",
          "svx.bridge.id" => bridge.id.to_s,
          "svx.bridge.name" => bridge.name,
          "com.docker.compose.project" => "",
          "com.docker.compose.service" => ""
        },
        Env: [
          "REFLECTOR_HOST=#{bridge.local_host}",
          "REFLECTOR_PORT=#{bridge.local_port}",
          "REFLECTOR_AUTH_KEY=#{bridge.local_auth_key}",
          "REFLECTOR_TG=#{bridge.local_default_tg}",
          "CALLSIGN=#{bridge.local_callsign}",
          "SIP_USERNAME=#{bridge.sip_username}",
          "SIP_PASSWORD=#{bridge.sip_password}",
          "SIP_SERVER=#{bridge.sip_server}",
          "SIP_PORT=#{bridge.sip_port || 5060}",
          "SIP_EXTENSION=#{bridge.sip_extension}",
          "SIP_TRANSPORT=#{bridge.sip_transport.presence || 'udp'}",
          "SIP_MODE=#{bridge.sip_mode.presence || 'persistent'}",
          "SIP_IDLE_TIMEOUT=#{bridge.sip_idle_timeout || 30}",
          "SIP_CODECS=#{bridge.sip_codecs.presence || 'opus,g722,gsm,ulaw,alaw'}",
          "SIP_DTMF=#{bridge.sip_dtmf}",
          "SIP_DTMF_DELAY=#{bridge.sip_dtmf_delay || 2000}",
          "SIP_CALLER_ID=#{bridge.sip_caller_id}",
          "SIP_LOG_LEVEL=#{bridge.sip_log_level || 1}",
          "SIP_PIN=#{bridge.sip_pin}",
          "SIP_PIN_TIMEOUT=#{bridge.sip_pin_timeout || 10}",
          "SIP_VOX_TIMEOUT=#{bridge.sip_vox_timeout || 3}",
          "SIP_PTT_KEY=#{bridge.sip_ptt_key.presence || '*'}",
          "SIP_MAX_CALL_DURATION=#{bridge.sip_max_call_duration || 180}",
          "NODE_LOCATION=#{bridge.node_location.presence || bridge.name}",
          "SYSOP=#{bridge.sysop}",
          "REDIS_URL=#{ENV.fetch('REDIS_URL', 'redis://redis:6379/1')}"
        ] + agc_env_array(bridge),
        HostConfig: {
          RestartPolicy: { Name: "unless-stopped" }
        }
      }
      body[:NetworkingConfig] = { EndpointsConfig: { network => {} } } if network

      result = docker_api_post_json("/containers/create?name=#{bridge.container_name}", body)
      if result && result["Id"]
        docker_api_post("/containers/#{result["Id"]}/start")
        Rails.logger.info "[Bridge] Created and started SIP container #{bridge.container_name}"
      end
    end

    def start_zello_container(bridge)
      bridge.generate_config
      network = docker_network
      bridge_dir = File.join(bridge_host_dir, bridge.id.to_s)
      body = {
        Image: "ghcr.io/audric/svxreflectordashboard-zello-bridge",
        Labels: {
          "svx.bridge" => "true",
          "svx.bridge.id" => bridge.id.to_s,
          "svx.bridge.name" => bridge.name,
          "com.docker.compose.project" => "",
          "com.docker.compose.service" => ""
        },
        Env: [
          "REFLECTOR_HOST=#{bridge.local_host}",
          "REFLECTOR_PORT=#{bridge.local_port}",
          "REFLECTOR_AUTH_KEY=#{bridge.local_auth_key}",
          "REFLECTOR_TG=#{bridge.local_default_tg}",
          "CALLSIGN=#{bridge.local_callsign}",
          "ZELLO_USERNAME=#{bridge.zello_username}",
          "ZELLO_PASSWORD=#{bridge.zello_password}",
          "ZELLO_CHANNEL=#{bridge.zello_channel}",
          "ZELLO_CHANNEL_PASSWORD=#{bridge.zello_channel_password}",
          "ZELLO_ISSUER_ID=#{bridge.zello_issuer_id}",
          "ZELLO_PRIVATE_KEY_FILE=/etc/zello/private_key.pem",
          "NODE_LOCATION=#{bridge.node_location.presence || bridge.name}",
          "SYSOP=#{bridge.sysop}",
          "REDIS_URL=#{ENV.fetch('REDIS_URL', 'redis://redis:6379/1')}"
        ] + agc_env_array(bridge),
        HostConfig: {
          Binds: ["#{bridge_dir}/zello_private_key.pem:/etc/zello/private_key.pem:ro"],
          RestartPolicy: { Name: "unless-stopped" }
        }
      }
      body[:NetworkingConfig] = { EndpointsConfig: { network => {} } } if network

      result = docker_api_post_json("/containers/create?name=#{bridge.container_name}", body)
      if result && result["Id"]
        docker_api_post("/containers/#{result["Id"]}/start")
        Rails.logger.info "[Bridge] Created and started Zello container #{bridge.container_name}"
      end
    end

    def start_svxlink_container(bridge)
      bridge.generate_config
      network = docker_network

      bridge_dir = File.join(bridge_host_dir, bridge.id.to_s)
      binds = [
        "#{bridge_dir}/svxlink.conf:/etc/svxlink/svxlink.conf",
        "#{bridge_dir}/node_info.json:/etc/svxlink/node_info.json:ro"
      ]
      if bridge.ca_bundle_path.exist?
        binds << "#{bridge_dir}/ca-bundle.crt:/var/lib/svxlink/pki/ca-bundle.crt:ro"
      end
      if bridge.echolink? && bridge.echolink_conf_path.exist?
        binds << "#{bridge_dir}/ModuleEchoLink.conf:/etc/svxlink/svxlink.d/ModuleEchoLink.conf:ro"
      end

      body = {
        Image: "ghcr.io/audric/svxlink-docker",
        Labels: {
          "svx.bridge" => "true",
          "svx.bridge.id" => bridge.id.to_s,
          "svx.bridge.name" => bridge.name,
          "com.docker.compose.project" => "",
          "com.docker.compose.service" => ""
        },
        Env: [
          "START_SVXLINK=1",
          "START_REMOTETRX=0",
          "START_SVXREFLECTOR=0",
          "LANG=C"
        ],
        HostConfig: {
          Binds: binds,
          RestartPolicy: { Name: "unless-stopped" }
        }
      }
      body[:NetworkingConfig] = { EndpointsConfig: { network => {} } } if network

      result = docker_api_post_json("/containers/create?name=#{bridge.container_name}", body)
      if result && result["Id"]
        docker_api_post("/containers/#{result["Id"]}/start")
        Rails.logger.info "[Bridge] Created and started container #{bridge.container_name}"
      end
    end

    def stop_container(bridge)
      container = find_container(bridge)
      return unless container
      docker_api_post("/containers/#{container["Id"]}/stop?t=5")
      Rails.logger.info "[Bridge] Stopped container #{bridge.container_name}"
    rescue => e
      Rails.logger.error "[Bridge] Failed to stop container: #{e.class} #{e.message}"
    end

    def restart_container(bridge)
      container = find_container(bridge)
      return start_or_recreate_container(bridge) unless container
      docker_api_post("/containers/#{container["Id"]}/restart?t=5")
      Rails.logger.info "[Bridge] Restarted container #{bridge.container_name}"
    rescue => e
      Rails.logger.error "[Bridge] Failed to restart container: #{e.class} #{e.message}"
    end

    def recreate_container(bridge)
      stop_container(bridge)
      remove_container(bridge)
      start_or_recreate_container(bridge)
    end

    def remove_container(bridge)
      container = find_container(bridge, all: true)
      return unless container
      docker_api_delete("/containers/#{container["Id"]}?force=true")
      Rails.logger.info "[Bridge] Removed container #{bridge.container_name}"
    rescue => e
      Rails.logger.error "[Bridge] Failed to remove container: #{e.class} #{e.message}"
    end

    def find_container(bridge, all: false)
      path = all ? "/containers/json?all=true" : "/containers/json"
      containers = docker_api_get(path)
      containers.find { |c| c["Names"].any? { |n| n == "/#{bridge.container_name}" } }
    end

    def fetch_container_statuses
      containers = docker_api_get("/containers/json?all=true")
      statuses = {}
      containers.each do |c|
        c["Names"].each do |n|
          if n =~ /\A\/(?:svxlink|xlx|dmr|ysf|allstar|zello|iax|sip)-bridge-(\d+)\z/
            statuses[Regexp.last_match(1).to_i] = c["State"]
          end
        end
      end
      statuses
    rescue => e
      Rails.logger.error "[Bridge] Failed to fetch container statuses: #{e.class} #{e.message}"
      {}
    end

    def fetch_container_logs(bridge, tail: 30)
      container = find_container(bridge, all: true)
      return nil unless container

      sock = UNIXSocket.new("/var/run/docker.sock")
      sock.write("GET /containers/#{container["Id"]}/logs?stdout=true&stderr=true&tail=#{tail}&timestamps=false HTTP/1.0\r\nHost: localhost\r\n\r\n")
      response = sock.read
      sock.close

      raw = response.force_encoding("BINARY").split("\r\n\r\n", 2).last
      # Docker multiplexed stream: 8-byte header per frame (type[1] + padding[3] + size[4])
      output = "".b
      pos = 0
      while pos + 8 <= raw.bytesize
        size = raw[pos + 4, 4].unpack1("N")
        break if pos + 8 + size > raw.bytesize
        output << raw[pos + 8, size]
        pos += 8 + size
      end
      output.encode("UTF-8", invalid: :replace, undef: :replace, replace: "")
    rescue => e
      Rails.logger.error "[Bridge] Failed to fetch logs: #{e.class} #{e.message}"
      nil
    end

    def bridge_host_dir
      # Resolve the host-side path of /rails/bridge by inspecting our own container's mounts
      hostname = Socket.gethostname
      containers = docker_api_get("/containers/json")
      self_container = containers.find { |c| c["Id"].start_with?(hostname) }
      if self_container
        inspect = docker_api_get("/containers/#{self_container["Id"]}/json")
        mounts = inspect["Mounts"] || []
        bridge_mount = mounts.find { |m| m["Destination"] == "/rails/bridge" }
        return bridge_mount["Source"] if bridge_mount

        # Dev override: full repo mounted at /rails
        rails_mount = mounts.find { |m| m["Destination"] == "/rails" && m["Type"] == "bind" }
        return File.join(rails_mount["Source"], "bridge") if rails_mount
      end
      # Fallback: assume standard layout
      File.join(Dir.pwd, "bridge")
    rescue => e
      Rails.logger.warn "[Bridge] Could not resolve host bridge path: #{e.message}, falling back"
      File.join(Dir.pwd, "bridge")
    end

    def agc_env_array(bridge)
      bridge.agc_env_lines.map { |l| l.to_s }
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
      Rails.logger.info "[Bridge] Docker API POST #{path}: #{response.split("\r\n").first}"
    end

    def docker_api_post_json(path, data)
      json = data.to_json
      sock = UNIXSocket.new("/var/run/docker.sock")
      sock.write("POST #{path} HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: #{json.bytesize}\r\n\r\n#{json}")
      response = sock.read
      sock.close
      body = response.split("\r\n\r\n", 2).last
      Rails.logger.info "[Bridge] Docker API POST #{path}: #{response.split("\r\n").first}"
      JSON.parse(body) rescue {}
    end

    def pull_image(image)
      if ENV['SKIP_IMAGE_PULL'].present?
        Rails.logger.info "[Bridge] Skipping pull for #{image} (SKIP_IMAGE_PULL set)"
        return
      end
      Rails.logger.info "[Bridge] Pulling image #{image}..."
      sock = UNIXSocket.new("/var/run/docker.sock")
      sock.write("POST /images/create?fromImage=#{image}&tag=latest HTTP/1.0\r\nHost: localhost\r\nContent-Length: 0\r\n\r\n")
      # Timeout after 30s to avoid blocking the request indefinitely
      if IO.select([sock], nil, nil, 30)
        response = sock.read_nonblock(65536) rescue ""
      else
        response = ""
        Rails.logger.warn "[Bridge] Pull #{image}: timeout (image may not exist in registry)"
      end
      sock.close
      Rails.logger.info "[Bridge] Pull #{image}: #{response.split("\r\n").first}" unless response.empty?
    rescue => e
      Rails.logger.warn "[Bridge] Failed to pull #{image}: #{e.message}"
    end

    def docker_api_delete(path)
      sock = UNIXSocket.new("/var/run/docker.sock")
      sock.write("DELETE #{path} HTTP/1.0\r\nHost: localhost\r\n\r\n")
      response = sock.read
      sock.close
      Rails.logger.info "[Bridge] Docker API DELETE #{path}: #{response.split("\r\n").first}"
    end
  end
end
