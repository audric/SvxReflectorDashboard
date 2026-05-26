-- Base ACL for the SVX Mumble server. Run ONCE against the server DB, then
-- restart the container. server_id=1, root channel_id=0.
--
-- Permission bits: Enter=4, Speak=8 (Enter+Speak=12).
-- The acl table columns are grantpriv/revokepriv (there is no id column; the
-- effective PK is server_id+channel_id+priority). The server ships default ACL
-- rows at priorities 1-3, so these custom rows use priority >= 4 and are
-- idempotent (safe to re-run).
--
-- Apply with:
--   docker compose exec mumble sh -c 'sqlite3 /data/mumble-server.sqlite < /dev/stdin' < db/mumble_acl_init.sql
--   docker compose restart mumble

-- Define the custom "tx" group on the root channel (admin/auth/all are built-in).
INSERT INTO groups (server_id, name, channel_id, inherit, inheritable)
SELECT 1, 'tx', 0, 1, 1
WHERE NOT EXISTS (SELECT 1 FROM groups WHERE server_id=1 AND channel_id=0 AND name='tx');

-- priority 4: deny Enter+Speak to everyone (revokepriv = 12).
INSERT INTO acl (server_id, channel_id, priority, user_id, group_name, apply_here, apply_sub, grantpriv, revokepriv)
SELECT 1, 0, 4, NULL, 'all', 1, 1, 0, 12
WHERE NOT EXISTS (SELECT 1 FROM acl WHERE server_id=1 AND channel_id=0 AND priority=4);

-- priority 5: grant Enter to registered users (auth) -> registered-only access.
INSERT INTO acl (server_id, channel_id, priority, user_id, group_name, apply_here, apply_sub, grantpriv, revokepriv)
SELECT 1, 0, 5, NULL, 'auth', 1, 1, 4, 0
WHERE NOT EXISTS (SELECT 1 FROM acl WHERE server_id=1 AND channel_id=0 AND priority=5);

-- priority 6: grant Speak to the tx group -> only can_transmit users may talk.
INSERT INTO acl (server_id, channel_id, priority, user_id, group_name, apply_here, apply_sub, grantpriv, revokepriv)
SELECT 1, 0, 6, NULL, 'tx', 1, 1, 8, 0
WHERE NOT EXISTS (SELECT 1 FROM acl WHERE server_id=1 AND channel_id=0 AND priority=6);
