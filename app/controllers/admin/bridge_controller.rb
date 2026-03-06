module Admin
  class BridgeController < ApplicationController
    layout false
    before_action :require_reflector_admin
    before_action :set_bridge, only: %i[edit update destroy toggle]

    def index
      @bridges = Bridge.order(:name)
      @container_statuses = fetch_container_statuses
    end

    def new
      @bridge = Bridge.new(
        local_host: "svxreflector",
        local_port: 5300,
        local_default_tg: 1,
        remote_port: 5300,
        remote_default_tg: 1,
        bridge_local_tg: 1,
        bridge_remote_tg: 1,
        timeout: 0
      )
    end

    def create
      @bridge = Bridge.new(bridge_params)
      if @bridge.save
        redirect_to admin_bridges_path, notice: "Bridge \"#{@bridge.name}\" created."
      else
        render :new, status: :unprocessable_entity
      end
    end

    def edit; end

    def update
      if @bridge.update(bridge_params)
        restart_container(@bridge) if @bridge.enabled?
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
        :name, :local_host, :local_port, :local_callsign, :local_auth_key,
        :local_default_tg, :remote_host, :remote_port, :remote_callsign,
        :remote_auth_key, :remote_default_tg, :bridge_local_tg,
        :bridge_remote_tg, :timeout, :enabled
      )
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
      # Check if container exists
      existing = find_container(bridge)
      if existing
        state = existing.dig("State") || ""
        if state == "running"
          restart_container(bridge)
        else
          docker_api_post("/containers/#{existing["Id"]}/start")
          Rails.logger.info "[Bridge] Started existing container #{bridge.container_name}"
        end
        return
      end

      # Create new container
      bridge.generate_config
      network = docker_network
      config_host_path = File.join(bridge_host_dir, bridge.id.to_s, "svxlink.conf")

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
          Binds: ["#{config_host_path}:/etc/svxlink/svxlink.conf"],
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
