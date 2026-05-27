require "json"
require "set"

# Read-only Mumble status for the admin System Info tab, sourced LIVE from the
# running Murmur server via its Ice interface — mumble_ice.py `status` exec'd in
# the mumble container over the Docker socket (see MumbleSync). No SQLite reads.
#
# snapshot returns { available:, reason:, rooms:, users:, online:, acls: }.
# available is false (with a reason: "no_container" / "unreachable" / "error")
# when Mumble isn't deployed or the server can't be reached, so the tab degrades
# gracefully instead of raising.
class MumbleStatus
  EMPTY = { rooms: [], users: [], online: [], acls: [] }.freeze

  def self.snapshot
    container = MumbleSync.find_mumble_container
    return EMPTY.merge(available: false, reason: "no_container") unless container

    out, err = MumbleSync.docker_exec_capture(container["Id"], ["python3", MumbleSync::ICE_SCRIPT, "status"])
    data = parse_json(out)
    unless data.is_a?(Hash) && data["available"]
      Rails.logger.warn("[MumbleStatus] unreachable: #{err.to_s.strip[-300..] || out.to_s[0, 200]}")
      return EMPTY.merge(available: false, reason: "unreachable")
    end

    bots = (MumbleSync.bot_callsigns rescue Set.new)
    users = Array(data["users"]).map { |u|
      type = u["userid"].to_i.zero? ? "superuser" : (bots.include?(u["name"].to_s.upcase) ? "bot" : "user")
      { name: u["name"], type: type, speak: !!u["speak"], admin: !!u["admin"],
        online: !!u["online"], room: u["room"] }
    }.sort_by { |u| [{ "superuser" => 0, "user" => 1, "bot" => 2 }.fetch(u[:type], 3), u[:name].to_s.downcase] }

    rooms = Array(data["rooms"]).map { |r|
      { id: r["id"], name: r["name"], root: !!r["root"], parent: r["parent"], online: r["online"].to_i }
    }.sort_by { |r| [r[:root] ? 0 : 1, r[:name].to_s.downcase] }

    online = Array(data["online"]).map { |o|
      { name: o["name"], room: o["room"], mute: !!o["mute"], deaf: !!o["deaf"],
        idle_secs: o["idle_secs"].to_i, registered: !!o["registered"] }
    }.sort_by { |o| [o[:room].to_s.downcase, o[:name].to_s.downcase] }

    acls = Array(data["acls"]).map { |a|
      { subject: a["subject"], scope: a["scope"], allow: Array(a["allow"]), deny: Array(a["deny"]) }
    }

    { available: true, reason: nil, rooms: rooms, users: users, online: online, acls: acls }
  rescue => e
    Rails.logger.error("[MumbleStatus] snapshot failed: #{e.message}")
    EMPTY.merge(available: false, reason: "error")
  end

  # The script prints JSON to stdout; Ice's slice warnings go to stderr (kept
  # separate by docker_exec_capture). Be defensive anyway.
  def self.parse_json(out)
    JSON.parse(out.to_s)
  rescue JSON::ParserError
    line = out.to_s.lines.reverse.find { |l| l.strip.start_with?("{") }
    line && (JSON.parse(line) rescue nil)
  end
end
