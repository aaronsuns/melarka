# Opt-in usage ping

Status: implemented 2026-11-10 (server `internal/usageping`, migration 0030, Admin → System, `site/worker.js`).

## Why

Melarka has no analytics, so the project cannot tell how many homes run it. Download counts include bots and
the author's own servers. An opt-in count answers "how many installs are running" without tracking anyone.

## What is sent

Only when an admin turns it on (Admin → System → "Send the weekly anonymous count"; off by default):

- `id`: 32 random hex characters, made when the ping is turned on, deleted when it is turned off (so turning
  it on again counts as a new install);
- `version`: the server's version.

`POST https://melarka.zexa.se/api/ping` with `{"id": "...", "version": "..."}`, once right after it is turned
on and then at most once every 7 days (checked hourly; a failed ping is retried at the next check). No user
names, counts of users or songs, paths or settings. The server does not send it from the apps; one ping per
install, not per person.

## Server

- Table `usage_ping` (one row, `id = 1`): `install_id`, `last_sent_at`. A row means on.
- `usageping.Service`: `Enabled`, `SetEnabled(on)` (on: insert a new id if none; off: delete the row),
  `SendIfDue`, `Run` (hourly ticker plus a wake-up when turned on). `last_sent_at` is only updated for the id
  that was sent, so turning it off during a request cannot bring the row back.
- API (admin only): `GET /api/v1/admin/usage-ping` → `{"enabled": bool}`; `PUT` with `{"enabled": bool}`.

## Website

`site/worker.js` handles `/api/ping` (POST only; validates the id and the version; at most 1 KB) and stores
`i:<id>` in the KV namespace `PINGS` with the version as metadata and a 35-day expiry, refreshed by each ping.
It never stores the IP address. `GET /api/ping/stats` returns `{"installs_35d": n, "versions": {...}}`: the
installs that pinged in the last 35 days. Anyone could post fake ids; the number is an estimate, not an audit.
