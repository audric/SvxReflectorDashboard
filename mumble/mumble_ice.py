#!/usr/bin/env python3
"""Live Mumble (Murmur) management over Ice — runs INSIDE the mumble container.

The dashboard execs this via the Docker socket instead of writing the server's
SQLite + restarting it, so registering/updating users and setting speak/admin
permissions applies to the LIVE server with no restart (nobody gets dropped).

Ice stays on 127.0.0.1:6502 inside the container (never exposed), so no Ice
secret is needed.

Verbs (all operate on virtual server id 1):
  sync          read desired state JSON from stdin, reconcile registrations
                + tx/admin group membership, print a JSON summary.
  getusers      print JSON of currently-connected users (live presence).
  getregistered print JSON {id: name} of registered accounts.

Desired-state JSON for `sync`:
  {"users": [{"name": "F4ABC", "password": "secret", "speak": true,
              "admin": false}, ...]}
Reconciliation is authoritative: registered accounts (except SuperUser id 0)
not present in `users` are unregistered.
"""
import sys
import json
import Ice

SERVER_ID = 1
ICE_PROXY = "Meta:tcp -h 127.0.0.1 -p 6502"
SLICE = "/usr/local/share/MumbleServer.ice"

Ice.loadSlice(["-I" + Ice.getSliceDir(), SLICE])  # zeroc-ice 3.8: argv-style list
import MumbleServer  # noqa: E402  (generated from the slice loaded above)

# Murmur ACL permission bits (ACL::Perm).
P_WRITE, P_ENTER, P_SPEAK, P_WHISPER, P_TEXT, P_MKTEMP, P_LISTEN = 1, 4, 8, 256, 512, 1024, 2048
LOCKDOWN_DENY = P_ENTER | P_SPEAK | P_WHISPER | P_TEXT | P_LISTEN  # 2828


def _base_acls():
    """Root-channel lockdown: registered (@auth) may Enter, only @tx may Speak,
    @admin gets Write, everyone else is denied. Mirrors db/mumble_acl_init.sql."""
    def acl(group, allow, deny):
        return MumbleServer.ACL(applyHere=True, applySubs=True, inherited=False,
                               userid=-1, group=group, allow=allow, deny=deny)
    return [
        acl("admin", P_WRITE, 0),
        acl("auth", P_MKTEMP, 0),
        acl("all", 0, LOCKDOWN_DENY),
        acl("auth", P_ENTER, 0),
        # tx may Speak and send text (the bridge bot needs TextMessage to post
        # the per-bridge welcome to users joining its channel).
        acl("tx", P_SPEAK | P_TEXT, 0),
    ]


# Full ACL permission bit -> name map, for decoding rules in `status`.
ACL_PERM_NAMES = [
    (0x00001, "Write"), (0x00002, "Traverse"), (0x00004, "Enter"), (0x00008, "Speak"),
    (0x00010, "MuteDeafen"), (0x00020, "Move"), (0x00040, "MakeChannel"),
    (0x00080, "LinkChannel"), (0x00100, "Whisper"), (0x00200, "TextMessage"),
    (0x00400, "MakeTempChannel"), (0x00800, "Listen"), (0x10000, "Kick"),
    (0x20000, "Ban"), (0x40000, "Register"), (0x80000, "SelfRegister"),
]


def _decode_perms(mask):
    mask = mask or 0
    return [name for bit, name in ACL_PERM_NAMES if mask & bit]


def _server(ic):
    meta = MumbleServer.MetaPrx.checkedCast(ic.stringToProxy(ICE_PROXY))
    if meta is None:
        raise RuntimeError("cannot reach Murmur Ice Meta at " + ICE_PROXY)
    server = meta.getServer(SERVER_ID)
    if server is None:
        raise RuntimeError("virtual server %d is not booted" % SERVER_ID)
    return server


def _set_group(groups, name, ids):
    """Point group `name`'s add-list at exactly `ids` (creating it if absent)."""
    ids = sorted(ids)
    for g in groups:
        if g.name == name:
            g.add = ids
            g.remove = []
            return groups
    groups.append(MumbleServer.Group(name=name, inherited=False, inherit=True,
                                     inheritable=True, add=ids, remove=[], members=[]))
    return groups


def _ensure_channels(server, channels):
    """Ensure each channel exists as a permanent child of Root (Ice-created
    channels persist, unlike the temporary ones bridge bots make) and apply its
    description (shown as the channel tooltip in the client)."""
    existing = {c.name: cid for cid, c in server.getChannels().items()}
    for ch in channels:
        name = (ch.get("name") or "").strip()
        if not name:
            continue
        cid = existing.get(name)
        if cid is None:
            cid = server.addChannel(name, 0)  # permanent, inherits Root ACL
        desc = ch.get("description")
        if desc is not None:
            state = server.getChannelState(cid)
            if state.description != desc:
                state.description = desc
                server.setChannelState(state)


def cmd_sync(server):
    # Payload comes as argv (single JSON string) when execd by the dashboard,
    # else from stdin for manual use.
    raw = sys.argv[2] if len(sys.argv) > 2 else sys.stdin.read()
    desired = json.loads(raw)
    welcome = desired.get("server_welcome")
    if welcome is not None:
        server.setConf("welcometext", welcome)  # live; shown on next connect
    _ensure_channels(server, desired.get("channels", []))
    users = desired.get("users", [])

    existing = server.getRegisteredUsers("")          # {id: name}
    by_name = {name.upper(): uid for uid, name in existing.items()}

    desired_names = set()
    speak_ids, admin_ids = set(), set()
    registered, updated = 0, 0

    for u in users:
        name = (u.get("name") or "").strip()
        if not name:
            continue
        key = name.upper()
        desired_names.add(key)
        info = {MumbleServer.UserInfo.UserName: name}
        if u.get("password"):
            info[MumbleServer.UserInfo.UserPassword] = u["password"]
        if key in by_name:
            uid = by_name[key]
            server.updateRegistration(uid, info)
            updated += 1
        else:
            uid = server.registerUser(info)
            by_name[key] = uid
            registered += 1
        if u.get("speak"):
            speak_ids.add(uid)
        if u.get("admin"):
            admin_ids.add(uid)

    # Authoritative: drop managed registrations that are no longer desired
    # (never touch SuperUser id 0).
    removed = 0
    for uid, name in existing.items():
        if uid == 0:
            continue
        if name.upper() not in desired_names:
            server.unregisterUser(uid)
            removed += 1

    # The root ACL is fully dashboard-managed: always (re)apply the canonical
    # base lockdown, then set tx/admin group membership. Idempotent; affects
    # only the root channel.
    _acls, groups, inherit = server.getACL(0)
    _set_group(groups, "tx", speak_ids | admin_ids)
    _set_group(groups, "admin", admin_ids)
    server.setACL(0, _base_acls(), groups, inherit)

    print(json.dumps({"registered": registered, "updated": updated,
                      "removed": removed, "speakers": len(speak_ids | admin_ids),
                      "admins": len(admin_ids)}))


def cmd_status(server):
    """One-shot snapshot for the admin tab: rooms, registered users (with
    speak/admin + live online state), the live connected list, and decoded
    root ACL — all read from the running server."""
    channels = server.getChannels()             # {id: Channel}
    online = list(server.getUsers().values())   # connected sessions
    reg = server.getRegisteredUsers("")         # {id: name}
    acls, groups, _inherit = server.getACL(0)

    chan_name = {cid: c.name for cid, c in channels.items()}

    def group_ids(name):
        for g in groups:
            if g.name == name:
                return set(g.members or []) | set(g.add or [])
        return set()
    speak_ids = group_ids("tx")
    admin_ids = group_ids("admin")

    online_by_uid = {}
    online_list = []
    counts = {}
    for u in online:
        counts[u.channel] = counts.get(u.channel, 0) + 1
        entry = {"name": u.name, "userid": u.userid, "room": chan_name.get(u.channel),
                 "mute": bool(u.mute or u.selfMute), "deaf": bool(u.deaf or u.selfDeaf),
                 "idle_secs": u.idlesecs, "registered": u.userid >= 0}
        online_list.append(entry)
        if u.userid >= 0:
            online_by_uid[u.userid] = entry

    rooms = []
    for cid, c in channels.items():
        is_root = (cid == 0) or (c.parent is None) or (c.parent < 0)
        rooms.append({"id": cid, "name": c.name, "root": is_root,
                      "parent": (None if is_root else chan_name.get(c.parent)),
                      "online": counts.get(cid, 0)})

    users = []
    for uid, name in reg.items():
        users.append({"name": name, "userid": uid,
                      "speak": (uid in speak_ids) or (uid in admin_ids),
                      "admin": uid in admin_ids,
                      "online": uid in online_by_uid,
                      "room": online_by_uid.get(uid, {}).get("room")})

    acl_out = []
    for a in acls:
        subject = ("user:%d" % a.userid) if (a.userid is not None and a.userid >= 0) else ("@" + a.group)
        scope = "+".join(s for s, on in (("here", a.applyHere), ("sub", a.applySubs)) if on) or "—"
        acl_out.append({"subject": subject, "scope": scope,
                        "allow": _decode_perms(a.allow), "deny": _decode_perms(a.deny)})

    print(json.dumps({"available": True, "rooms": rooms, "users": users,
                      "online": online_list, "acls": acl_out}))


def main():
    if len(sys.argv) < 2:
        sys.stderr.write("usage: mumble_ice.py {sync|status}\n")
        return 2
    cmd = sys.argv[1]
    ic = Ice.initialize()
    try:
        server = _server(ic)
        if cmd == "sync":
            cmd_sync(server)
        elif cmd == "status":
            cmd_status(server)
        else:
            sys.stderr.write("unknown command: %s\n" % cmd)
            return 2
    finally:
        ic.destroy()
    return 0


if __name__ == "__main__":
    sys.exit(main())
