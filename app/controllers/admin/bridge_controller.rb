module Admin
  class BridgeController < ApplicationController
    layout false
    before_action :require_reflector_admin
    before_action :set_bridge, only: %i[edit update destroy toggle backups]

    def index
      @bridges = Bridge.includes(:bridge_tg_mappings).order(:name)
      @container_statuses = fetch_container_statuses
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
          echolink_servers: "servers.echolink.org"
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
        :echolink_servers, :default_active
      )
    end

    def save_tg_mappings(bridge)
      bridge.bridge_tg_mappings.destroy_all
      local_tgs = Array(params.dig(:bridge, :mapping_local_tgs))
      remote_tgs = Array(params.dig(:bridge, :mapping_remote_tgs))
      timeouts = Array(params.dig(:bridge, :mapping_timeouts))
      default_actives = Array(params.dig(:bridge, :mapping_default_actives))
      activate_on_activities = Array(params.dig(:bridge, :mapping_activate_on_activities))
      local_tgs.zip(remote_tgs, timeouts, default_actives, activate_on_activities).each do |local_tg, remote_tg, tout, active, activate_on|
        next if local_tg.blank? || remote_tg.blank?
        bridge.bridge_tg_mappings.create(
          local_tg: local_tg.to_i, remote_tg: remote_tg.to_i,
          timeout: tout.to_i, default_active: active == "1",
          activate_on_activity: activate_on.presence
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
        # Always recreate to pick up config/mount changes
        stop_container(bridge) if existing["State"] == "running"
        remove_container(bridge)
      end

      # Create new container
      bridge.generate_config
      network = docker_network
      config_host_path = File.join(bridge_host_dir, bridge.id.to_s, "svxlink.conf")

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
        Image: "audric/svxlink",
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
    rescue => e
      Rails.logger.error "[Bridge] Failed to start container: #{e.class} #{e.message}"
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
          if n =~ /\A\/svxlink-bridge-(\d+)\z/
            statuses[Regexp.last_match(1).to_i] = c["State"]
          end
        end
      end
      statuses
    rescue => e
      Rails.logger.error "[Bridge] Failed to fetch container statuses: #{e.class} #{e.message}"
      {}
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

    def docker_api_delete(path)
      sock = UNIXSocket.new("/var/run/docker.sock")
      sock.write("DELETE #{path} HTTP/1.0\r\nHost: localhost\r\n\r\n")
      response = sock.read
      sock.close
      Rails.logger.info "[Bridge] Docker API DELETE #{path}: #{response.split("\r\n").first}"
    end
  end
end
