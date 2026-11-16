# Quick account switch

Status: implemented 2026-11-16 (migration 0032, `internal/auth` links, `/auth/accounts`, `/auth/switch`, Settings → Accounts).

## Why

A parent's phone is often handed to a child to play music. Their plays would then change the parent's "For you"
and history, and their hearts the parent's favorites. Signing out and back in with a long password takes too
long, so nobody does it. Aaron's call (2026-11-16): switching is one tap, with no PIN or password, on any device
where both accounts have signed in once.

## Behaviour

- **Settings → Accounts** lists the accounts signed in on this device, the current one marked. Tapping another
  switches to it at once. **Add account** signs in to one more account (username and password, once) and switches
  to it. Each other account has a remove button that signs it out on this device only.
- **Sign out** signs out every account on this device.
- Switching remounts the signed-in app (`PrefsProvider key={user.id}`), so queues, preferences, last played and
  pending play events stay per user, as after a sign-in. In the iPhone and Android apps the page posts the
  usual `auth` message with the new `userId`, so native switches user as on any sign-in (`switchUser`: the old
  user's playback and queues are reset; offline caches are already per user and stay on disk).
- No PIN: anyone holding the device can switch to any account added there, including an admin's.

## Server

- `devices.link_group` (migration 0032): devices with the same non-null value were signed in on the same
  browser or app and may switch to each other. At most one device per user in a group.
- `POST /auth/login` with `"link": true` and a valid session: after the password check the new device joins the
  current device's group (made on first use), replacing any older device of that user in the group, and the
  response sets the cookie to the new session. Signing in to the account that is already current returns 409
  `account_already_here` (no device is left behind).
- `GET /auth/accounts` → `[{user_id, username, current}]` (just the current account without a group).
- `POST /auth/switch {"user_id"}` → the target device's token is replaced by a new one (tokens are stored only as
  hashes, so the old one cannot be handed out again), the cookie is set to it, and `{token, user}` is returned.
  404 `not_linked` for a user not in the group. The device switched away from keeps its token, so a request
  still in flight with it does not get a 401.
- `DELETE /auth/accounts/{user_id}` signs that account out on this device; 400 for the current one.
- `POST /auth/logout` deletes the current device and every device in its group.
