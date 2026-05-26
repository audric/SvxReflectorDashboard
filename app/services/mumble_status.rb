require "sqlite3"
require "set"

# Read-only view of the local Mumble server's SQLite DB, for the admin
# System Info "Mumble" tab. The write side (creating users, ACL groups,
# channels) lives in MumbleSync; this class only reads and never restarts
# the server.
#
# Note: the DB holds *stored* state, not live sessions. `last_active`,
# `last_disconnect` and `lastchannel` are the last known values Murmur
# persisted — they approximate "last seen" / "last room", not who is
# connected right now.
class MumbleStatus
  SERVER_ID = 1

  # Murmur ACL permission bits (ACL::Perm) -> human names, for decoding the
  # grantpriv/revokepriv bitmasks in the acl table.
  ACL_PERMS = {
    0x00001 => "Write",
    0x00002 => "Traverse",
    0x00004 => "Enter",
    0x00008 => "Speak",
    0x00010 => "MuteDeafen",
    0x00020 => "Move",
    0x00040 => "MakeChannel",
    0x00080 => "LinkChannel",
    0x00100 => "Whisper",
    0x00200 => "TextMessage",
    0x00400 => "MakeTempChannel",
    0x00800 => "Listen",
    0x10000 => "Kick",
    0x20000 => "Ban",
    0x40000 => "Register",
    0x80000 => "SelfRegister",
  }.freeze

  # Returns { available:, rooms:, users:, acls: } (plus :error on failure).
  # available:false when the server DB doesn't exist yet, so the tab can
  # render a friendly "not initialised" note instead of erroring.
  def self.snapshot
    path = MumbleSync.db_path
    return { available: false, rooms: [], users: [], acls: [] } unless File.exist?(path)

    # Open read-write like MumbleSync (we only issue SELECTs). A read-only
    # open fails with "unable to open database file" on a WAL-mode DB when the
    # process can't write the containing directory, which is the case here.
    db = SQLite3::Database.new(path)
    db.results_as_hash = true
    # Murmur holds the DB open; wait briefly for its write locks rather than
    # failing immediately with SQLITE_BUSY.
    db.busy_timeout = 5000
    begin
      { available: true, rooms: rooms(db), users: users(db), acls: acls(db) }
    ensure
      db.close
    end
  rescue => e
    Rails.logger.warn("[MumbleStatus] read failed: #{e.message}")
    { available: false, rooms: [], users: [], acls: [], error: e.message }
  end

  # Channel tree: Root first, then children sorted by name. Each row carries
  # how many registered users were last seen in it (from users.lastchannel).
  def self.rooms(db)
    chans = db.execute("SELECT channel_id, parent_id, name, inheritacl FROM channels WHERE server_id = ?", SERVER_ID)
    names = chans.to_h { |c| [c["channel_id"], c["name"]] }

    seen_counts = Hash.new(0)
    db.execute("SELECT lastchannel, COUNT(*) AS n FROM users WHERE server_id = ? AND lastchannel IS NOT NULL GROUP BY lastchannel", SERVER_ID)
      .each { |r| seen_counts[r["lastchannel"]] = r["n"] }

    chans.map { |c|
      {
        id: c["channel_id"],
        name: c["name"],
        parent: c["parent_id"] && names[c["parent_id"]],
        root: c["parent_id"].nil?,
        inherit_acl: c["inheritacl"] == 1,
        last_seen_count: seen_counts[c["channel_id"]],
      }
    }.sort_by { |c| [c[:root] ? 0 : 1, c[:name].to_s.downcase] }
  end

  # Registered users with derived type and speak/admin permission. Type is
  # inferred from the user_id range MumbleSync assigns: 0 = SuperUser,
  # >= BOT_USER_ID_BASE = a bridge bot, otherwise a dashboard user.
  def self.users(db)
    bot_base  = MumbleSync::BOT_USER_ID_BASE
    chan_names = db.execute("SELECT channel_id, name FROM channels WHERE server_id = ?", SERVER_ID).to_h { |c| [c["channel_id"], c["name"]] }
    admins   = group_member_ids(db, "admin")
    speakers = group_member_ids(db, "tx")

    db.execute("SELECT user_id, name, last_active, last_disconnect, lastchannel FROM users WHERE server_id = ?", SERVER_ID).map { |u|
      uid = u["user_id"]
      type = if uid.zero? then "superuser"
             elsif uid >= bot_base then "bot"
             else "user"
             end
      {
        id: uid,
        name: u["name"],
        type: type,
        admin: admins.include?(uid),
        # admins always speak (they're added to the tx group too), but OR for safety.
        can_speak: speakers.include?(uid) || admins.include?(uid),
        last_active: u["last_active"],
        last_disconnect: u["last_disconnect"],
        last_room: u["lastchannel"] && chan_names[u["lastchannel"]],
      }
    }.sort_by { |u| [{ "superuser" => 0, "user" => 1, "bot" => 2 }.fetch(u[:type], 3), u[:name].to_s.downcase] }
  end

  # Set of user_ids belonging (addit=1) to a named root-channel group.
  def self.group_member_ids(db, group_name)
    gid = db.get_first_value("SELECT group_id FROM groups WHERE server_id = ? AND channel_id = 0 AND name = ?", [SERVER_ID, group_name])
    return Set.new unless gid
    db.execute("SELECT user_id FROM group_members WHERE server_id = ? AND group_id = ? AND addit = 1", [SERVER_ID, gid])
      .map { |r| r["user_id"] }.to_set
  end

  # Per-channel ACL rules, in evaluation order (priority). Each rule applies to
  # a group (e.g. @all, @auth, @tx) or a specific user, scoped to this channel
  # and/or its subtree, granting/denying a decoded set of permissions.
  def self.acls(db)
    chan_names = db.execute("SELECT channel_id, name FROM channels WHERE server_id = ?", SERVER_ID).to_h { |c| [c["channel_id"], c["name"]] }
    user_names = db.execute("SELECT user_id, name FROM users WHERE server_id = ?", SERVER_ID).to_h { |u| [u["user_id"], u["name"]] }

    db.execute("SELECT channel_id, priority, user_id, group_name, apply_here, apply_sub, grantpriv, revokepriv FROM acl WHERE server_id = ? ORDER BY channel_id, priority", SERVER_ID).map { |a|
      subject = a["user_id"] ? "user: #{user_names[a["user_id"]] || a["user_id"]}" : "@#{a["group_name"]}"
      scope = [("here" if a["apply_here"] == 1), ("sub" if a["apply_sub"] == 1)].compact.join("+")
      {
        channel: chan_names[a["channel_id"]] || a["channel_id"],
        priority: a["priority"],
        subject: subject,
        scope: scope.presence || "—",
        allow: decode_perms(a["grantpriv"]),
        deny: decode_perms(a["revokepriv"]),
      }
    }
  end

  # Decode a Murmur ACL permission bitmask into the list of permission names.
  def self.decode_perms(mask)
    m = mask.to_i
    return [] if m.zero?
    ACL_PERMS.filter_map { |bit, name| name if (m & bit) != 0 }
  end
end
