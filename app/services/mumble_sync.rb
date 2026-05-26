require "sqlite3"
require "digest"
require "socket"
require "json"

# Syncs allow_mumble users into the local Mumble server's SQLite DB.
# Source of truth = dashboard. Writes registered users + admin/tx ACL-group
# membership, removes stale rows, then restarts the mumble container.
#
# Assumes the base ACL + the "tx" group already exist on the root channel
# (created once by db/mumble_acl_init.sql).
class MumbleSync
  SERVER_ID = 1
  BOT_USER_ID_BASE = 100_000 # bot accounts get ids >= this to avoid colliding with human users

  def self.db_path
    ENV.fetch("MUMBLE_DB_PATH", "/mumble_data/mumble-server.sqlite")
  end

  def self.sync_users
    return unless File.exist?(db_path)

    db = SQLite3::Database.new(db_path)
    db.results_as_hash = true
    # The mumble-server holds the DB open in rollback-journal mode; wait for its
    # brief write locks instead of failing immediately with SQLITE_BUSY.
    db.busy_timeout = 5000
    begin
      ensure_base_acl(db)
      ensure_channels(db)
      admin_group_id = group_id(db, "admin")
      tx_group_id    = group_id(db, "tx")

      # Desired human accounts: callsign(uppercase) => {id, pw_sha1, admin, tx}
      desired = {}
      User.where(allow_mumble: true).where.not(mumble_password: [nil, ""]).find_each do |u|
        next if u.callsign.blank?
        desired[u.callsign.upcase] = {
          id: 1_000 + u.id, # stable, > SuperUser(0), < bot base
          pw: Digest::SHA1.hexdigest(u.mumble_password.to_s),
          admin: u.role == "admin",
          tx: u.can_transmit
        }
      end

      # Bot accounts for each mumble bridge: BRIDGE_CALLSIGN => {id, pw}
      Bridge.where(bridge_type: "mumble").find_each do |b|
        next if b.local_callsign.blank? || b.mumble_bot_password.blank?
        desired[b.local_callsign.upcase] = {
          id: BOT_USER_ID_BASE + b.id,
          pw: Digest::SHA1.hexdigest(b.mumble_bot_password.to_s),
          admin: false,
          tx: true # the bot must be able to inject TG audio
        }
      end

      db.transaction do
        # Remove human/bot rows we manage that are no longer desired (never touch SuperUser id 0).
        existing = db.execute("SELECT user_id, name FROM users WHERE server_id = ? AND user_id > 0", SERVER_ID)
        existing.each do |row|
          next if desired.key?(row["name"].to_s.upcase)
          uid = row["user_id"]
          db.execute("DELETE FROM users WHERE server_id = ? AND user_id = ?", [SERVER_ID, uid])
          db.execute("DELETE FROM group_members WHERE server_id = ? AND user_id = ?", [SERVER_ID, uid])
          db.execute("DELETE FROM user_info WHERE server_id = ? AND user_id = ?", [SERVER_ID, uid]) rescue nil
        end

        desired.each do |name, info|
          # Upsert the user row. salt='' and kdfiterations=0 => legacy SHA1 mode.
          db.execute("INSERT OR REPLACE INTO users (server_id, user_id, name, pw, salt, kdfiterations) VALUES (?, ?, ?, ?, '', 0)",
                     [SERVER_ID, info[:id], name, info[:pw]])
          # Reset group membership for this user, then re-add as needed.
          db.execute("DELETE FROM group_members WHERE server_id = ? AND user_id = ?", [SERVER_ID, info[:id]])
          db.execute("INSERT INTO group_members (group_id, server_id, user_id, addit) VALUES (?, ?, ?, 1)",
                     [admin_group_id, SERVER_ID, info[:id]]) if info[:admin] && admin_group_id
          # admins can always speak (the root Speak deny applies to non-tx); the tx
          # group grants Speak, so admins and can_transmit users both belong to it.
          db.execute("INSERT INTO group_members (group_id, server_id, user_id, addit) VALUES (?, ?, ?, 1)",
                     [tx_group_id, SERVER_ID, info[:id]]) if (info[:tx] || info[:admin]) && tx_group_id
        end
      end
    ensure
      db.close
    end

    # Reached only if the transaction committed (an exception above propagates
    # past this point), so we never restart on a failed write.
    restart_mumble
  end

  # Idempotently ensures the base ACL (the `tx` group, registered-only entry,
  # and tx-only speak) exists. Runs on every sync so a fresh deployment is
  # locked down automatically the first time a user/bridge is synced — no
  # manual SQL step. Statements live in db/mumble_acl_init.sql (re-runnable).
  def self.ensure_base_acl(db)
    sql_path = Rails.root.join("db", "mumble_acl_init.sql")
    return unless File.exist?(sql_path)
    db.execute_batch(File.read(sql_path))
  rescue => e
    Rails.logger.error "[MumbleSync] Failed to ensure base ACL: #{e.message}"
  end

  # Ensures each mumble bridge's target channel exists as a PERMANENT child of
  # Root, with inheritacl=1 so the root lockdown (registered-only Enter, tx-only
  # Speak) applies to it. Permanent channels survive bridge restarts, so joined
  # listeners stay put instead of being bounced to Root each time the bot's old
  # temporary channel vanished. Idempotent: matched by name, otherwise created
  # with the next free channel_id (Mumble resumes its counter from max+1 on boot).
  def self.ensure_channels(db)
    Bridge.where(bridge_type: "mumble").find_each do |b|
      name = b.mumble_channel.to_s.strip
      next if name.blank?
      row = db.get_first_row("SELECT channel_id FROM channels WHERE server_id = ? AND parent_id = 0 AND name = ?",
                             [SERVER_ID, name])
      if row
        db.execute("UPDATE channels SET inheritacl = 1 WHERE server_id = ? AND channel_id = ?",
                   [SERVER_ID, row["channel_id"]])
      else
        next_id = db.get_first_value("SELECT COALESCE(MAX(channel_id), 0) + 1 FROM channels WHERE server_id = ?",
                                     [SERVER_ID]).to_i
        db.execute("INSERT INTO channels (server_id, channel_id, parent_id, name, inheritacl) VALUES (?, ?, 0, ?, 1)",
                   [SERVER_ID, next_id, name])
      end
    end
  rescue => e
    Rails.logger.error "[MumbleSync] Failed to ensure channels: #{e.message}"
  end

  # Returns the group_id of a named root-channel (channel_id 0) group, or nil.
  def self.group_id(db, name)
    row = db.get_first_row("SELECT group_id FROM groups WHERE server_id = ? AND channel_id = 0 AND name = ?",
                           [SERVER_ID, name])
    row && row["group_id"]
  end

  def self.restart_mumble
    container = find_mumble_container
    return unless container
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("POST /containers/#{container["Id"]}/restart?t=5 HTTP/1.0\r\nHost: localhost\r\nContent-Length: 0\r\n\r\n")
    sock.read
    sock.close
    Rails.logger.info "[MumbleSync] Restarted mumble container"
  rescue => e
    Rails.logger.error "[MumbleSync] Failed to restart mumble: #{e.message}"
  end

  def self.find_mumble_container
    sock = UNIXSocket.new("/var/run/docker.sock")
    sock.write("GET /containers/json HTTP/1.0\r\nHost: localhost\r\n\r\n")
    response = sock.read
    sock.close
    body = response.split("\r\n\r\n", 2).last
    containers = JSON.parse(body)
    containers.find { |c| c["Names"].any? { |n| n =~ /-mumble-\d+$/ || n == "/mumble" } }
  end
end
