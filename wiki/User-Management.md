# User Management

## Default admin account

On first boot, `db:seed` creates a default admin:

| Callsign | Password |
|---|---|
| `ADM1N` | `changeme` |

Log in at `/login` and change the password immediately.

## Registration

New users register at `/register`. The registration form requires:

- **Callsign** — must be a valid amateur radio callsign format (e.g. `W1AW`, `KA1ABC`, `VE3XYZ`), max 8 characters
- **Password** — minimum 8 characters

Callsigns are automatically uppercased and validated against the format `/\A[A-Z0-9]{1,3}\d[A-Z]{1,4}\z/`.

New registrations are held in a **pending** state until approved by an admin.

## Web Users admin panel

Admins manage web users at `/admin/users`. Available actions:

- **Create** new users directly (pre-approved, no registration needed)
- **Approve** pending registrations
- **Edit** user profile, permissions, and role
- **Delete** user accounts (admins cannot delete themselves)

### User profile fields

| Field | Required | Description |
|---|---|---|
| Callsign | Yes | Amateur radio callsign (auto-uppercased, unique) |
| Password | Yes | Minimum 8 characters |
| Name | No | Display name |
| Email | No | Contact email |
| Mobile | No | Phone number |
| Telegram | No | Telegram handle |

## Roles

| Role | Description |
|---|---|
| `admin` | Full access to all features and the admin panel (user management, bridges, system info) |
| `user` | Standard user, subject to per-user permissions below |

## Permissions

| Permission | Default | What it allows |
|---|---|---|
| `can_monitor` | Off | Tune in to talkgroups and receive audio |
| `can_transmit` | Off | Use Push-to-Talk to transmit via the reflector |
| `reflector_admin` | Off | Access to reflector configuration at `/admin/reflector` (edit global settings, certificates, users, passwords, TG rules) |
| `cw_roger_beep` | Off | Enable CW roger beep on transmit |
| `reflector_auth_key` | — | Per-user authentication key for the reflector |

Both `can_monitor` and `can_transmit` default to off for new users and must be explicitly granted by an admin after approval.

### Reflector admin

The `reflector_admin` permission is separate from the `admin` role. An admin user manages web users, bridges, and system settings. A reflector admin can additionally configure the SVXReflector itself.

The system enforces that **at least one reflector admin must exist at all times** — you cannot remove the last reflector admin's permission.

### Self-protection

Admins editing their own account cannot change their:
- Callsign
- Role
- Reflector admin status

This prevents accidental lockout.

## Command-line operations

Useful for production when you can't access the web UI (e.g. locked out of the admin account).

### Change a user's password

```bash
docker compose exec web bin/rails runner '
  u = User.find_by!(callsign: "ADM1N")
  u.update!(password: "newpassword", password_confirmation: "newpassword")
  puts "Password updated for #{u.callsign}"
'
```

### Create a new admin user

```bash
docker compose exec web bin/rails runner '
  User.create!(callsign: "W1AW", password: "securepass", password_confirmation: "securepass", role: "admin", approved: true)
  puts "Admin user created"
'
```

### Approve a pending user

```bash
docker compose exec web bin/rails runner '
  u = User.find_by!(callsign: "F4ABC")
  u.update!(approved: true, can_monitor: true)
  puts "#{u.callsign} approved with monitor permission"
'
```

### List all users

```bash
docker compose exec web bin/rails runner '
  User.all.each { |u| puts "#{u.callsign.ljust(10)} role=#{u.role} approved=#{u.approved?} monitor=#{u.can_monitor} tx=#{u.can_transmit} refl_admin=#{u.reflector_admin}" }
'
```
