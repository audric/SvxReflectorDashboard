class Bridge < ApplicationRecord
  has_many :bridge_tg_mappings, dependent: :destroy

  ALL_BRIDGE_TYPES = %w[reflector echolink xlx dmr ysf allstar].freeze
  BRIDGE_TYPES = (ENV.fetch('BRIDGE_TYPES', ALL_BRIDGE_TYPES.join(',')).split(',').map(&:strip) & ALL_BRIDGE_TYPES).freeze

  validates :name, presence: true, uniqueness: true
  validates :bridge_type, presence: true, inclusion: { in: BRIDGE_TYPES }
  validates :local_host, presence: true
  validates :local_port, presence: true, numericality: { greater_than: 0 }
  validates :local_callsign, presence: true
  validates :local_auth_key, presence: true
  validates :local_default_tg, presence: true, numericality: { greater_than: 0 }

  # Reflector-specific validations
  with_options if: :reflector? do
    validates :remote_host, presence: true
    validates :remote_port, presence: true, numericality: { greater_than: 0 }
    validates :remote_callsign, presence: true
    validates :remote_auth_key, presence: true
    validates :remote_default_tg, presence: true, numericality: { greater_than: 0 }
  end

  # EchoLink-specific validations
  with_options if: :echolink? do
    validates :echolink_callsign, presence: true
    validates :echolink_password, presence: true
  end

  # XLX-specific validations
  with_options if: :xlx? do
    validates :xlx_host, presence: true
    validates :xlx_module, presence: true, format: { with: /\A[A-Z]\z/, message: "must be a letter A-Z" }
    validates :xlx_callsign, presence: true, length: { maximum: 7 }
    validates :xlx_callsign_suffix, presence: true, format: { with: /\A[A-Z]\z/, message: "must be a letter A-Z" }
  end

  # DMR-specific validations
  with_options if: :dmr? do
    validates :dmr_host, presence: true
    validates :dmr_id, presence: true, numericality: { greater_than: 0 }
    validates :dmr_password, presence: true
    validates :dmr_talkgroup, presence: true, numericality: { greater_than: 0 }
    validates :dmr_timeslot, presence: true, inclusion: { in: [1, 2], message: "must be 1 or 2" }
    validates :dmr_color_code, numericality: { greater_than_or_equal_to: 0, less_than_or_equal_to: 15, message: "must be 0-15" }, allow_nil: true
  end

  # YSF-specific validations
  with_options if: :ysf? do
    validates :ysf_host, presence: true
  end

  # AllStar-specific validations
  with_options if: :allstar? do
    validates :allstar_node, presence: true
    validates :allstar_password, presence: true
    validates :allstar_server, presence: true
  end

  # Returns the 8-char DCS callsign: base callsign (space-padded to 7) + suffix letter.
  def dcs_callsign
    "%-7s%s" % [xlx_callsign.to_s.upcase, xlx_callsign_suffix.to_s.upcase]
  end

  after_save :generate_config
  after_destroy :cleanup

  def reflector?
    bridge_type == "reflector"
  end

  def echolink?
    bridge_type == "echolink"
  end

  def xlx?
    bridge_type == "xlx"
  end

  def dmr?
    bridge_type == "dmr"
  end

  def ysf?
    bridge_type == "ysf"
  end

  def allstar?
    bridge_type == "allstar"
  end

  def config_dir
    Rails.root.join("bridge", id.to_s)
  end

  def config_path
    config_dir.join("svxlink.conf")
  end

  def container_name
    if xlx?
      "xlx-bridge-#{id}"
    elsif dmr?
      "dmr-bridge-#{id}"
    elsif ysf?
      "ysf-bridge-#{id}"
    elsif allstar?
      "allstar-bridge-#{id}"
    else
      "svxlink-bridge-#{id}"
    end
  end

  MAX_BACKUPS = 10

  def generate_config
    FileUtils.mkdir_p(config_dir, mode: 0o755)
    backup_configs

    if xlx?
      generate_xlx_config
    elsif dmr?
      generate_dmr_config
    elsif ysf?
      generate_ysf_config
    elsif allstar?
      generate_allstar_config
    elsif echolink?
      generate_echolink_config
    else
      generate_reflector_config
    end
    write_node_info unless xlx? || dmr? || ysf? || allstar?
  end

  def echolink_conf_path
    config_dir.join("ModuleEchoLink.conf")
  end

  def node_info_path
    config_dir.join("node_info.json")
  end

  def write_node_info
    info = {
      nodeClass: echolink? ? "echolink" : "bridge",
      nodeLocation: node_location.presence || name,
      hidden: false,
      sysop: sysop.presence
    }.compact

    if reflector? && bridge_tg_mappings.any?
      info[:links] = bridge_tg_mappings.reload.map do |m|
        { localTg: m.local_tg, remoteTg: m.remote_tg }
      end
      info[:remoteHost] = remote_host if remote_host.present?
    end

    File.write(node_info_path, JSON.pretty_generate(info))
  end

  def ca_bundle_path
    config_dir.join("ca-bundle.crt")
  end

  def write_ca_bundle
    local_ca = fetch_local_ca_bundle
    remote_ca = remote_ca_bundle.to_s.strip

    if local_ca.blank? && remote_ca.blank?
      File.delete(ca_bundle_path) if ca_bundle_path.exist?
      return
    end

    parts = [local_ca, remote_ca].select(&:present?)
    File.write(ca_bundle_path, parts.join("\n") + "\n")
  end

  def backups_dir
    config_dir.join("backups")
  end

  private

  def reflector_logic_lines
    write_ca_bundle
    has_ca_bundle = ca_bundle_path.exist?

    shared_lines = ["NODE_INFO_FILE=/etc/svxlink/node_info.json"]
    shared_lines << "JITTER_BUFFER_DELAY=#{jitter_buffer_delay}" if jitter_buffer_delay.present?
    shared_lines << "MONITOR_TGS=#{monitor_tgs}" if monitor_tgs.present?
    shared_lines << "TG_SELECT_TIMEOUT=#{tg_select_timeout}" if tg_select_timeout.present?
    shared_lines << "MUTE_FIRST_TX_LOC=#{mute_first_tx_loc? ? 1 : 0}"
    shared_lines << "MUTE_FIRST_TX_REM=#{mute_first_tx_rem? ? 1 : 0}"
    shared_lines << "VERBOSE=0" if verbose == false
    shared_lines << "UDP_HEARTBEAT_INTERVAL=#{udp_heartbeat_interval}" if udp_heartbeat_interval.present?
    shared_lines << "CA_BUNDLE_FILE=/var/lib/svxlink/pki/ca-bundle.crt" if has_ca_bundle

    cert_lines = []
    cert_lines << "CERT_SUBJ_C=#{cert_subj_c}" if cert_subj_c.present?
    cert_lines << "CERT_SUBJ_O=#{cert_subj_o}" if cert_subj_o.present?
    cert_lines << "CERT_SUBJ_OU=#{cert_subj_ou}" if cert_subj_ou.present?
    cert_lines << "CERT_SUBJ_L=#{cert_subj_l}" if cert_subj_l.present?
    cert_lines << "CERT_SUBJ_ST=#{cert_subj_st}" if cert_subj_st.present?
    cert_lines << "CERT_SUBJ_GN=#{cert_subj_gn}" if cert_subj_gn.present?
    cert_lines << "CERT_SUBJ_SN=#{cert_subj_sn}" if cert_subj_sn.present?
    cert_lines << "CERT_EMAIL=#{cert_email}" if cert_email.present?

    { shared: shared_lines, cert: cert_lines }
  end

  def generate_xlx_config
    lines = []
    lines << "# XLX Bridge configuration (passed as env vars to container)"
    lines << "REFLECTOR_HOST=#{local_host}"
    lines << "REFLECTOR_PORT=#{local_port}"
    lines << "REFLECTOR_AUTH_KEY=#{local_auth_key}"
    lines << "REFLECTOR_TG=#{local_default_tg}"
    lines << "CALLSIGN=#{local_callsign}"
    lines << "XLX_HOST=#{xlx_host}"
    lines << "XLX_PORT=#{xlx_port || 30051}"
    lines << "XLX_MODULE=#{xlx_module}"
    lines << "XLX_REFLECTOR_NAME=#{xlx_reflector_name.presence || 'XLX000'}"
    lines << "DCS_CALLSIGN=#{dcs_callsign}"
    lines << "DCS_MYCALL=#{xlx_mycall}" if xlx_mycall.present?
    lines << "DCS_MYCALL_SUFFIX=#{xlx_mycall_suffix.presence || 'AMBE'}"
    lines << "NODE_LOCATION=#{node_location.presence || name}"
    lines << "SYSOP=#{sysop}" if sysop.present?
    lines << ""
    File.write(config_dir.join("xlx_bridge.env"), lines.join("\n"))
  end

  def generate_dmr_config
    lines = []
    lines << "# DMR Bridge configuration (passed as env vars to container)"
    lines << "REFLECTOR_HOST=#{local_host}"
    lines << "REFLECTOR_PORT=#{local_port}"
    lines << "REFLECTOR_AUTH_KEY=#{local_auth_key}"
    lines << "REFLECTOR_TG=#{local_default_tg}"
    lines << "CALLSIGN=#{local_callsign}"
    lines << "DMR_HOST=#{dmr_host}"
    lines << "DMR_PORT=#{dmr_port || 62030}"
    lines << "DMR_ID=#{dmr_id}"
    lines << "DMR_PASSWORD=#{dmr_password}"
    lines << "DMR_TALKGROUP=#{dmr_talkgroup}"
    lines << "DMR_TIMESLOT=#{dmr_timeslot || 2}"
    lines << "DMR_COLOR_CODE=#{dmr_color_code || 1}"
    lines << "DMR_CALLSIGN=#{dmr_callsign}" if dmr_callsign.present?
    lines << "NODE_LOCATION=#{node_location.presence || name}"
    lines << "SYSOP=#{sysop}" if sysop.present?
    lines << ""
    File.write(config_dir.join("dmr_bridge.env"), lines.join("\n"))
  end

  def generate_ysf_config
    lines = []
    lines << "# YSF Bridge configuration (passed as env vars to container)"
    lines << "REFLECTOR_HOST=#{local_host}"
    lines << "REFLECTOR_PORT=#{local_port}"
    lines << "REFLECTOR_AUTH_KEY=#{local_auth_key}"
    lines << "REFLECTOR_TG=#{local_default_tg}"
    lines << "CALLSIGN=#{local_callsign}"
    lines << "YSF_HOST=#{ysf_host}"
    lines << "YSF_PORT=#{ysf_port || 42000}"
    lines << "YSF_CALLSIGN=#{ysf_callsign.presence || local_callsign}"
    lines << "YSF_DESCRIPTION=#{ysf_description}" if ysf_description.present?
    lines << "NODE_LOCATION=#{node_location.presence || name}"
    lines << "SYSOP=#{sysop}" if sysop.present?
    lines << ""
    File.write(config_dir.join("ysf_bridge.env"), lines.join("\n"))
  end

  def generate_allstar_config
    lines = []
    lines << "# AllStar Bridge configuration (passed as env vars to container)"
    lines << "REFLECTOR_HOST=#{local_host}"
    lines << "REFLECTOR_PORT=#{local_port}"
    lines << "REFLECTOR_AUTH_KEY=#{local_auth_key}"
    lines << "REFLECTOR_TG=#{local_default_tg}"
    lines << "CALLSIGN=#{local_callsign}"
    lines << "ALLSTAR_NODE=#{allstar_node}"
    lines << "ALLSTAR_PASSWORD=#{allstar_password}"
    lines << "ALLSTAR_SERVER=#{allstar_server}"
    lines << "ALLSTAR_PORT=#{allstar_port || 4569}"
    lines << "NODE_LOCATION=#{node_location.presence || name}"
    lines << "SYSOP=#{sysop}" if sysop.present?
    lines << ""
    File.write(config_dir.join("allstar_bridge.env"), lines.join("\n"))
  end

  def generate_reflector_config
    mappings = bridge_tg_mappings.reload
    opts = reflector_logic_lines
    lines = []

    link_names = mappings.each_with_index.map { |_, i| "Link#{i + 1}" }
    lines << "[GLOBAL]"
    lines << "LOGICS=ReflectorLogicLocal,ReflectorLogicRemote"
    lines << "LINKS=#{link_names.join(",")}"
    lines << "TIMESTAMP_FORMAT=\"%c\""
    lines << "CARD_SAMPLE_RATE=48000"
    lines << ""

    lines << "[ReflectorLogicLocal]"
    lines << "TYPE=ReflectorV2"
    lines << "HOSTS=#{local_host}"
    lines << "HOST_PORT=#{local_port}"
    lines << "CALLSIGN=#{local_callsign}"
    lines << "AUTH_KEY=#{local_auth_key}"
    lines << "DEFAULT_TG=#{local_default_tg}"
    lines.concat(opts[:shared])
    lines.concat(opts[:cert])
    lines << ""

    lines << "[ReflectorLogicRemote]"
    lines << "TYPE=ReflectorV2"
    lines << "HOSTS=#{remote_host}"
    lines << "HOST_PORT=#{remote_port}"
    lines << "CALLSIGN=#{remote_callsign}"
    lines << "AUTH_KEY=#{remote_auth_key}"
    lines << "DEFAULT_TG=#{remote_default_tg}"
    lines.concat(opts[:shared])
    lines.concat(opts[:cert])

    mappings.each_with_index do |mapping, i|
      lines << ""
      lines << "[Link#{i + 1}]"
      lines << "CONNECT_LOGICS=ReflectorLogicLocal:#{mapping.local_tg},ReflectorLogicRemote:#{mapping.remote_tg}"
      lines << "DEFAULT_ACTIVE=#{mapping.default_active != false ? 1 : 0}"
      lines << "TIMEOUT=#{mapping.timeout || 0}"
    end

    lines << ""
    File.write(config_path, lines.join("\n"))
  end

  def generate_echolink_config
    opts = reflector_logic_lines
    lines = []

    lines << "[GLOBAL]"
    lines << "LOGICS=SimplexLogic,ReflectorLogicLocal"
    lines << "LINKS=Link1"
    lines << "TIMESTAMP_FORMAT=\"%c\""
    lines << "CARD_SAMPLE_RATE=48000"
    lines << "CFG_DIR=/etc/svxlink/svxlink.d"
    lines << ""

    # SimplexLogic hosts the EchoLink module
    lines << "[SimplexLogic]"
    lines << "TYPE=Simplex"
    lines << "RX=Rx1"
    lines << "TX=Tx1"
    lines << "MODULES=ModuleEchoLink"
    lines << "CALLSIGN=#{echolink_callsign}"
    lines << "EVENT_HANDLER=/usr/share/svxlink/events.tcl"
    lines << "DEFAULT_LANG=en_US"
    lines << ""

    # Null audio devices (no physical radio)
    lines << "[Rx1]"
    lines << "TYPE=Net"
    lines << "HOST=localhost"
    lines << "TCP_PORT=5210"
    lines << "CODEC=OPUS"
    lines << ""

    lines << "[Tx1]"
    lines << "TYPE=Net"
    lines << "HOST=localhost"
    lines << "TCP_PORT=5220"
    lines << "CODEC=OPUS"
    lines << ""

    # ReflectorLogic connects to our local reflector
    lines << "[ReflectorLogicLocal]"
    lines << "TYPE=ReflectorV2"
    lines << "HOSTS=#{local_host}"
    lines << "HOST_PORT=#{local_port}"
    lines << "CALLSIGN=#{local_callsign}"
    lines << "AUTH_KEY=#{local_auth_key}"
    lines << "DEFAULT_TG=#{local_default_tg}"
    lines.concat(opts[:shared])
    lines.concat(opts[:cert])
    lines << ""

    # Link SimplexLogic (EchoLink) to ReflectorLogic
    lines << "[Link1]"
    lines << "CONNECT_LOGICS=SimplexLogic,ReflectorLogicLocal:#{local_default_tg}"
    lines << "DEFAULT_ACTIVE=#{default_active != false ? 1 : 0}"
    lines << "TIMEOUT=0"
    lines << ""

    File.write(config_path, lines.join("\n"))

    # Write ModuleEchoLink.conf
    generate_echolink_module_conf
  end

  def generate_echolink_module_conf
    lines = []
    lines << "[ModuleEchoLink]"
    lines << "NAME=EchoLink"
    lines << "ID=2"
    lines << "TIMEOUT=60"
    lines << "SERVERS=#{echolink_servers.presence || "servers.echolink.org"}"
    lines << "CALLSIGN=#{echolink_callsign}"
    lines << "PASSWORD=#{echolink_password}"
    lines << "SYSOPNAME=#{echolink_sysopname}" if echolink_sysopname.present?
    lines << "LOCATION=#{echolink_location}" if echolink_location.present?
    lines << "MAX_QSOS=#{echolink_max_qsos || 10}"
    lines << "MAX_CONNECTIONS=#{echolink_max_connections || 11}"
    lines << "LINK_IDLE_TIMEOUT=#{echolink_link_idle_timeout || 300}"

    # Proxy settings
    lines << "PROXY_SERVER=#{echolink_proxy_server}" if echolink_proxy_server.present?
    lines << "PROXY_PORT=#{echolink_proxy_port}" if echolink_proxy_port.present?
    lines << "PROXY_PASSWORD=#{echolink_proxy_password}" if echolink_proxy_password.present?

    # Auto-connect
    lines << "AUTOCON_ECHOLINK_ID=#{echolink_autocon_echolink_id}" if echolink_autocon_echolink_id.present?
    lines << "AUTOCON_TIME=#{echolink_autocon_time}" if echolink_autocon_time.present?

    # Access control
    lines << "ACCEPT_INCOMING=#{echolink_accept_incoming}" if echolink_accept_incoming.present?
    lines << "REJECT_INCOMING=#{echolink_reject_incoming}" if echolink_reject_incoming.present?
    lines << "DROP_INCOMING=#{echolink_drop_incoming}" if echolink_drop_incoming.present?
    lines << "ACCEPT_OUTGOING=#{echolink_accept_outgoing}" if echolink_accept_outgoing.present?
    lines << "REJECT_OUTGOING=#{echolink_reject_outgoing}" if echolink_reject_outgoing.present?
    lines << "REJECT_CONF=1" if echolink_reject_conf?
    lines << "USE_GSM_ONLY=1" if echolink_use_gsm_only?
    lines << "BIND_ADDR=#{echolink_bind_addr}" if echolink_bind_addr.present?

    # Description
    if echolink_description.present?
      desc_lines = echolink_description.split("\n").map { |l| "\"#{l}\\n\"" }
      lines << "DESCRIPTION=#{desc_lines.join("\n\t    ")}"
    end

    lines << ""
    File.write(echolink_conf_path, lines.join("\n"))
  end

  def fetch_local_ca_bundle
    # Read the local reflector's CA bundle from the reflector container
    containers = docker_api_get("/containers/json")
    container = containers.find { |c| c["Names"].any? { |n| n =~ /-svxreflector-\d+$/ } }
    return nil unless container

    result = docker_api_post_json("/containers/#{container["Id"]}/exec", {
      Cmd: ["cat", "/var/lib/svxlink/pki/ca-bundle.crt"],
      AttachStdout: true, AttachStderr: true
    })
    exec_id = result["Id"]
    return nil unless exec_id

    start_body = { Detach: false, Tty: false }.to_json
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("POST /exec/#{exec_id}/start HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: #{start_body.bytesize}\r\n\r\n#{start_body}")
    response = sock.read
    sock.close

    raw = response.split("\r\n\r\n", 2).last
    parse_exec_output(raw).presence
  rescue => e
    Rails.logger.warn "[Bridge] Could not fetch local CA bundle: #{e.message}"
    nil
  end

  def docker_api_get(path)
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("GET #{path} HTTP/1.0\r\nHost: localhost\r\n\r\n")
    response = sock.read
    sock.close
    body = response.split("\r\n\r\n", 2).last
    JSON.parse(body)
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

  ARCHIVE_RETENTION_DAYS = 30

  def self.archive_dir
    Rails.root.join("bridge", "_archive")
  end

  def cleanup
    return unless config_dir.exist?

    archive_name = "#{id}_#{name.parameterize}_#{Time.current.strftime('%Y%m%d_%H%M%S')}"
    dest = self.class.archive_dir.join(archive_name)
    FileUtils.mkdir_p(self.class.archive_dir)
    FileUtils.mv(config_dir, dest)

    self.class.purge_old_archives
  end

  def self.purge_old_archives
    return unless archive_dir.exist?

    cutoff = ARCHIVE_RETENTION_DAYS.days.ago
    Dir.glob(archive_dir.join("*")).each do |dir|
      next unless File.directory?(dir)
      FileUtils.rm_rf(dir) if File.mtime(dir) < cutoff
    end
  end

  def backup_configs
    self.class.purge_old_archives

    files = [config_path, node_info_path, echolink_conf_path,
             config_dir.join("xlx_bridge.env"), config_dir.join("dmr_bridge.env"),
             config_dir.join("ysf_bridge.env"), config_dir.join("allstar_bridge.env")].select(&:exist?)
    return if files.empty?

    migrate_legacy_backups if Dir.glob(config_dir.join("*.bak")).any?

    stamp = Time.current.strftime("%Y%m%d_%H%M%S")
    snapshot_dir = backups_dir.join(stamp)
    FileUtils.mkdir_p(snapshot_dir)
    files.each { |path| FileUtils.cp(path, snapshot_dir.join(File.basename(path))) }

    # Prune old snapshots
    snapshots = Dir.glob(backups_dir.join("*")).select { |d| File.directory?(d) }.sort
    excess = snapshots.size - MAX_BACKUPS
    snapshots.first(excess).each { |d| FileUtils.rm_rf(d) } if excess > 0
  end

  def migrate_legacy_backups
    Dir.glob(config_dir.join("*.bak")).each do |path|
      basename = File.basename(path)
      stamp = basename.match(/\.(\d{8}_\d{6})\.bak$/)&.[](1)
      next unless stamp

      config_name = basename.sub(/\.\d{8}_\d{6}\.bak$/, "")
      snapshot_dir = backups_dir.join(stamp)
      FileUtils.mkdir_p(snapshot_dir)
      FileUtils.mv(path, snapshot_dir.join(config_name))
    end
  end
end
