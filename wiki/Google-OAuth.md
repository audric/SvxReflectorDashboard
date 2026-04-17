# Google OAuth

The dashboard can accept Google accounts as a sign-in method alongside the built-in callsign/password login. This page walks through creating the Google Cloud credentials, configuring the dashboard, and understanding the sign-up flow.

OAuth is entirely optional — if `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` are not set, the **Sign in with Google** button is hidden and the app behaves exactly as before.

## Overview

When a user clicks **Sign in with Google**:

1. They are redirected to Google to consent.
2. Google redirects back to `/auth/google_oauth2/callback` with an authorization code.
3. The dashboard exchanges the code for the user's profile (email + display name).
4. Three things can happen:
   - **Known OAuth user** (same `provider` + `uid`) → logged in.
   - **Existing user with matching email** → the Google identity is linked and they are logged in.
   - **New user** → redirected to `/auth/complete` to enter their amateur radio callsign. The account is created in **pending** state and must be approved by an admin.

Because password authentication is skipped for OAuth users, the `password_digest` column is nullable and the model's password validations only run when `provider` is blank.

## 1. Create a Google Cloud project

1. Go to the [Google Cloud Console](https://console.cloud.google.com/).
2. Create a new project (or reuse an existing one) — any name is fine, e.g. `svx-dashboard`.
3. In the left sidebar, open **APIs & Services** → **OAuth consent screen**.
4. Choose **External** as the user type, click **Create**.
5. Fill in the required fields:
   - **App name** — e.g. `SVX Dashboard`
   - **User support email** — your address
   - **Developer contact** — your address
6. On the **Scopes** page, no extra scopes are needed — the defaults (`email`, `profile`, `openid`) are enough.
7. On the **Test users** page, add your own Google email while the app is in **Testing** mode. Only listed test users can sign in until the app is published.

## 2. Create OAuth credentials

1. Open **APIs & Services** → **Credentials**.
2. Click **Create Credentials** → **OAuth client ID**.
3. Application type: **Web application**.
4. Name: e.g. `SVX Dashboard Web`.
5. Add **Authorized JavaScript origins**:
   - Production: `https://your-domain.example.com`
   - Local dev (optional): `http://localhost:3000`
6. Add **Authorized redirect URIs**:
   - Production: `https://your-domain.example.com/auth/google_oauth2/callback`
   - Local dev (optional): `http://localhost:3000/auth/google_oauth2/callback`

   The path is always `/auth/google_oauth2/callback`. Scheme, host, port and path must match **exactly** — no trailing slash, no query string.

7. Click **Create**. Google shows your **Client ID** and **Client Secret** — copy both.

Changes to redirect URIs can take up to a minute to propagate on Google's side.

## 3. Configure the dashboard

Add the credentials to your `.env`:

```
GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your-client-secret
```

Restart the web service:

```bash
docker compose up -d --force-recreate web
```

The login and registration pages will now show a **Sign in with Google** button.

## 4. Sign-up flow

1. User clicks **Sign in with Google** on `/login` or `/register`.
2. After consenting on Google, they land on `/auth/complete`.
3. The form shows their Google name and email and asks for a **callsign**.
4. The account is created with `approved: false` — an admin must approve it at `/admin/users` before the user can sign in.
5. On subsequent visits, Google sign-in logs them straight in (no callsign prompt).

The admin users list shows an **Auth** column indicating whether a user signed up via password or Google.

## Linking an existing password account

If a user already has a password-based account with the same email as their Google account, signing in with Google will automatically link the two and log them in. After linking, they can use either method.

The link is based on the email address returned by Google. If the emails don't match, the Google sign-in creates a separate account.

## Troubleshooting

### `Error 400: redirect_uri_mismatch`

The redirect URI sent by the dashboard doesn't match anything registered in Google Cloud Console.

- Verify the full URL, including scheme (`http` vs `https`), host, port, and path.
- The path must be exactly `/auth/google_oauth2/callback`.
- Add both your production URL and `http://localhost:3000/...` if you develop locally.
- Wait ~1 minute after saving in Google Cloud for propagation.

### `Access blocked: This app's request is invalid`

The OAuth consent screen isn't fully configured, or the signing-in user isn't on the test user list (while the app is in Testing mode).

- Complete the OAuth consent screen form.
- Add the user's email under **Test users**, or publish the app.

### `Google authentication failed` after clicking the button

Usually a missing or mistyped `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET`. Check the container logs:

```bash
docker compose logs -f web
```

Also make sure the env vars are actually set inside the container:

```bash
docker compose exec web env | grep GOOGLE_
```

### User gets "Password can't be blank" after entering their callsign

This was a bug fixed in `e7b0694` — update to the latest code. The session was storing OAuth data with symbol keys while the controller read it with string keys, so the new user was created without a `provider` and the password validation kicked in.

### Changing your display name

The `name` field is populated from `info.name` in Google's response **only when the account is first created**. Later changes to your Google profile are not re-synced. Edit the user at `/admin/users/:id/edit` to change the stored name.

## Security notes

- Sessions are signed and encrypted with `SECRET_KEY_BASE`. Rotating that value invalidates all existing sessions, including OAuth-linked ones.
- The OAuth client secret is sensitive — never commit it or expose it to the browser. It lives only in `.env` on the server.
- New OAuth accounts are always created as **pending** and with `role: user`, `can_monitor: false`, `can_transmit: false`. Grant permissions explicitly from the admin panel.
- OAuth does not bypass the [audio permissions flow](User-Management#setting-up-audio-tune-in-and-ptt) — a new user still needs a reflector auth key before Tune In works.
