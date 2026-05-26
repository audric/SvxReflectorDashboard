-- Base ACL for the SVX Mumble server. Run ONCE against the server DB, then
-- restart the container. server_id=1, root channel_id=0.
--
-- Permission bits: Enter=4, Speak=8, Whisper=0x100=256, TextMessage=0x200=512,
-- Listen=0x800=2048 (sum=2828), SelfRegister=0x80000=524288.
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

-- priority 4: deny Enter+Speak+Whisper+Listen+TextMessage to everyone
-- (revokepriv = 2828). Beyond normal channel voice this also blocks shout/
-- whisper (256), the listen-without-joining feature (2048), and text chat
-- (512), so an unregistered guest can neither inject audio into a channel it
-- cannot enter, monitor one, nor message anyone. The UPDATE makes this
-- self-healing: it widens an existing prio-4 row created with an earlier mask.
INSERT INTO acl (server_id, channel_id, priority, user_id, group_name, apply_here, apply_sub, grantpriv, revokepriv)
SELECT 1, 0, 4, NULL, 'all', 1, 1, 0, 2828
WHERE NOT EXISTS (SELECT 1 FROM acl WHERE server_id=1 AND channel_id=0 AND priority=4);
UPDATE acl SET grantpriv = 0, revokepriv = 2828 WHERE server_id=1 AND channel_id=0 AND priority=4;

-- priority 5: grant Enter to registered users (auth) -> registered-only access.
INSERT INTO acl (server_id, channel_id, priority, user_id, group_name, apply_here, apply_sub, grantpriv, revokepriv)
SELECT 1, 0, 5, NULL, 'auth', 1, 1, 4, 0
WHERE NOT EXISTS (SELECT 1 FROM acl WHERE server_id=1 AND channel_id=0 AND priority=5);

-- priority 6: grant Speak to the tx group -> only can_transmit users may talk.
INSERT INTO acl (server_id, channel_id, priority, user_id, group_name, apply_here, apply_sub, grantpriv, revokepriv)
SELECT 1, 0, 6, NULL, 'tx', 1, 1, 8, 0
WHERE NOT EXISTS (SELECT 1 FROM acl WHERE server_id=1 AND channel_id=0 AND priority=6);

-- priority 7: revoke SelfRegister from everyone -> a kicked/revoked user cannot
-- self-register a new account to regain @auth (and thus channel entry). The
-- server's default priority-3 row grants this to @all on root; this overrides it.
INSERT INTO acl (server_id, channel_id, priority, user_id, group_name, apply_here, apply_sub, grantpriv, revokepriv)
SELECT 1, 0, 7, NULL, 'all', 1, 0, 0, 524288
WHERE NOT EXISTS (SELECT 1 FROM acl WHERE server_id=1 AND channel_id=0 AND priority=7);
