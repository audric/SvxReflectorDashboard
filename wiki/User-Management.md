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

## User roles

| Role | Description |
|---|---|
| `admin` | Full access to all features and the admin panel |
| `user` | Standard user, subject to monitor/transmit permissions |
