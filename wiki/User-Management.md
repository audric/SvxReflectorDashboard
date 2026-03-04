# User Management

## Default admin account

On first boot, `db:seed` creates a default admin:

| Callsign | Password |
|---|---|
| `ADMIN` | `changeme` |

Log in at `/login` and change the password immediately.

## Registration

New users register at `/register`. The registration form requires:

- **Callsign** — must be a valid amateur radio callsign format (e.g. `W1AW`, `KA1ABC`, `VE3XYZ`), max 8 characters
- **Password** — minimum 8 characters

Callsigns are automatically uppercased and validated against the format `/\A[A-Z0-9]{1,3}\d[A-Z]{1,4}\z/`.

New registrations are held in a **pending** state until approved by an admin.

## Admin panel

Admins manage users at `/admin/users`. Available actions:

- **Approve** pending registrations
- **Grant/revoke monitor permission** — allows tuning in to talkgroups
- **Grant/revoke transmit permission** — allows Push-to-Talk
- **Promote to admin** — grants full admin access
- **Delete** user accounts

## Permissions

| Permission | What it allows |
|---|---|
| `can_monitor` | Tune in to talkgroups and receive audio |
| `can_transmit` | Use Push-to-Talk to transmit via the reflector |
| `admin` | Full access: user management, settings, all features |

Both `can_monitor` and `can_transmit` default to `false` for new users and must be explicitly granted by an admin after approval.

## Command-line operations

Useful for production when you can't access the web UI (e.g. locked out of the admin account).

### Change a user's password

```bash
docker compose exec web bin/rails runner '
  u = User.find_by!(callsign: "ADMIN")
  u.update!(password: "newpassword", password_confirmation: "newpassword")
  puts "Password updated for #{u.callsign}"
'
```

### Create a new admin user

```bash
docker compose exec web bin/rails runner '
  User.create!(callsign: "W1AW", password: "securepass", password_confirmation: "securepass", role: "admin")
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
  User.all.each { |u| puts "#{u.callsign.ljust(10)} role=#{u.role} approved=#{u.approved?} monitor=#{u.can_monitor} tx=#{u.can_transmit}" }
'
```

## User roles

| Role | Description |
|---|---|
| `admin` | Full access to all features and the admin panel |
| `user` | Standard user, subject to monitor/transmit permissions |
