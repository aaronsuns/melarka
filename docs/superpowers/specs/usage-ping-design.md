# Usage ping (anonymous install count)

Status: implemented 2026-11-10 as opt-in (migration 0030); on by default since 2026-11-13 (0.2.22, migration 0031),
with coarse numbers added (server `internal/usageping`, Admin → System, `site/worker.js`).

## Why

Melarka has no analytics, so the project cannot tell how many homes run it. Download counts include bots and
the author's own servers. An opt-in count answers "how many installs are running" without tracking anyone.

## What is sent

On by default, for new installs and, through migration 0031, for existing ones on upgrade (Aaron's decision
2026-11-13: more complete numbers, with every field listed in the README "Privacy" section). An admin turns it
off in Admin → System → "Send the weekly anonymous count"; `LARK_USAGE_PING=off` keeps it off for good.

- `id`: 32 random hex characters (migration 0031 makes the first one; turning the switch off deletes it, so
  turning it on again counts as a new install);
- `version`: the server's version;
- `os`, `arch`: Go's `runtime.GOOS` / `runtime.GOARCH`;
- `container`: whether `/.dockerenv` exists;
- `users`: a range of the user count (`1`, `2-5`, `6-20`, `21+`);
- `tracks`: a range of the non-trashed tracks (`0`, `1-99`, `100-999`, `1000-9999`, `10000+`).

`POST https://melarka.zexa.se/api/ping` with that JSON, at the first hourly check after start and then at
most once every 7 days (a failed ping is retried at the next check). No user names, exact counts, songs,
history, paths or settings. The apps send nothing; one ping per install, not per person. The log says the
count is on at every start while it is.

Legal basis (GDPR legitimate interest): the data is minimal, the id is random and not linked to a person,
the site keeps no IP address, it is documented in the README, and turning it off is one click or one
variable. The README must list every field; a new field needs a README change in the same commit.

## Server

- Table `usage_ping` (one row, `id = 1`): `install_id`, `last_sent_at`. A row means on. Migration 0031
  inserts the row once (default on, upgrades too); nothing re-inserts it after an admin deletes it.
- `LARK_USAGE_PING=off` (`config.UsagePing`, `Service.Off`): `Run` deletes the row and returns,
  `Enabled` is false, `SetEnabled(true)` returns `ErrOff` (API 409 `usage_ping_env_off`).
- `usageping.Service`: `Enabled`, `SetEnabled(on)` (on: insert a new id if none; off: delete the row),
  `SendIfDue`, `Run` (hourly ticker plus a wake-up when turned on). `last_sent_at` is only updated for the id
  that was sent, so turning it off during a request cannot bring the row back.
- API (admin only): `GET /api/v1/admin/usage-ping` → `{"enabled": bool, "env_off": bool}`; `PUT` with `{"enabled": bool}`.

## Website

`site/worker.js` handles `/api/ping` (POST only; validates the id and the version; at most 1 KB) and stores
`i:<id>` in the KV namespace `PINGS` with the version and the coarse fields as metadata and a 35-day expiry, refreshed by each ping.
It never stores the IP address. `GET /api/ping/stats` returns `{"installs_35d": n, "versions": {...}, "os": {...}, "arch": {...}, "container": {...}, "users": {...}, "tracks": {...}}`: the
installs that pinged in the last 35 days. Anyone could post fake ids; the number is an estimate, not an audit.
