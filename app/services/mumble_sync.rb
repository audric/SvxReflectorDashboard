require "socket"
require "json"
require "set"

# Syncs dashboard users + mumble bridge bots into the *running* Mumble server
# through its Ice management interface, by exec'ing mumble_ice.py inside the
# mumble container (over the Docker socket). Registrations, speak/admin group
# membership, the base lockdown ACL, and bridge channels are applied to the
# LIVE server — no restart — so bridges and connected clients are never dropped.
# Source of truth = the dashboard.
class MumbleSync
  ICE_SCRIPT = "/usr/local/bin/mumble_ice.py".freeze

  # Apply the desired state to the live server via Ice. Never restarts.
  def self.sync_users
    container = find_mumble_container
    unless container
      Rails.logger.warn "[MumbleSync] mumble container not found; skipping sync"
      return
    end
    out, err = docker_exec_capture(container["Id"], ["python3", ICE_SCRIPT, "sync", desired_state.to_json])
    if out.to_s.strip.empty?
      Rails.logger.error "[MumbleSync] live sync produced no result: #{err.to_s.strip[-500..] || err}"
    else
      Rails.logger.info "[MumbleSync] live sync: #{out.strip}"
    end
  rescue => e
    Rails.logger.error "[MumbleSync] sync failed: #{e.message}"
  end

  # Desired Mumble accounts (allow_mumble users + bridge bots) and the channels
  # bridges need. Passwords are sent as plaintext (over the local Docker socket);
  # Murmur hashes them per its config.
  def self.desired_state
    users = []
    User.where(allow_mumble: true).where.not(mumble_password: [nil, ""]).find_each do |u|
      next if u.callsign.blank?
      users << { name: u.callsign.upcase, password: u.mumble_password.to_s,
                 speak: !!u.can_transmit, admin: u.role == "admin" }
    end
    channels = []
    Bridge.where(bridge_type: "mumble").find_each do |b|
      next if b.local_callsign.blank? || b.mumble_bot_password.blank?
      # The bot must speak (inject TG audio); never an admin.
      users << { name: b.mumble_username.to_s.upcase, password: b.mumble_bot_password.to_s,
                 speak: true, admin: false }
      next if b.mumble_channel.blank?
      channels << { name: b.mumble_channel.to_s.strip, description: b.mumble_description.to_s }
    end
    state = { users: users, channels: channels.uniq { |c| c[:name] } }
    # Server-wide welcome message (shown in the client on connect). Include it
    # whenever configured (even blank, so it can be cleared); omit if never set.
    welcome = Setting.get("mumble_welcome", nil)
    state[:server_welcome] = welcome unless welcome.nil?
    state
  end

  # Uppercase callsigns of the mumble bridge bot accounts — lets readers (e.g.
  # the System Info tab) tell bot rows from human users without relying on the
  # user-id range, since Murmur assigns its own ids via Ice.
  def self.bot_callsigns
    Bridge.where(bridge_type: "mumble")
          .map { |b| b.mumble_username.to_s.strip.upcase }.reject(&:empty?).to_set
  end

  def self.find_mumble_container
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("GET /containers/json HTTP/1.0\r\nHost: localhost\r\n\r\n")
    body = sock.read.split("\r\n\r\n", 2).last
    sock.close
    JSON.parse(body).find { |c| c["Names"].any? { |n| n =~ /-mumble-\d+$/ || n == "/mumble" } }
  rescue => e
    Rails.logger.error "[MumbleSync] container lookup failed: #{e.message}"
    nil
  end

  # Exec a command in a container via the Docker socket and return its
  # [stdout, stderr], de-multiplexing Docker's exec stream (per frame: byte 0 =
  # stream id 1=stdout/2=stderr, bytes 4..7 = big-endian payload length).
  def self.docker_exec_capture(container_id, cmd, timeout: 8)
    create = { Cmd: cmd, AttachStdout: true, AttachStderr: true }.to_json
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.timeout = timeout if sock.respond_to?(:timeout=)
    sock.write("POST /containers/#{container_id}/exec HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: #{create.bytesize}\r\n\r\n#{create}")
    body = sock.read.split("\r\n\r\n", 2).last
    sock.close
    exec_id = (JSON.parse(body) rescue {})["Id"]
    return [nil, "no exec id"] unless exec_id

    start = { Detach: false, Tty: false }.to_json
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.timeout = timeout if sock.respond_to?(:timeout=)
    sock.write("POST /exec/#{exec_id}/start HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: #{start.bytesize}\r\n\r\n#{start}")
    body = sock.read.split("\r\n\r\n", 2).last
    sock.close

    stdout = +""
    stderr = +""
    pos = 0
    while pos + 8 <= body.bytesize
      stream = body.getbyte(pos)
      size = body.byteslice(pos + 4, 4).unpack1("N")
      break if pos + 8 + size > body.bytesize
      (stream == 2 ? stderr : stdout) << body.byteslice(pos + 8, size)
      pos += 8 + size
    end
    [stdout.force_encoding("UTF-8").scrub, stderr.force_encoding("UTF-8").scrub]
  end
end
