---
name: lark-tagging
description: Use when the user asks to tag their Melarka (lark) music library (or "tag the new songs", "tag my Melarka library") - reads not-yet-reviewed tracks from the Melarka (lark) server and writes tags chosen only from Melarka's vocabulary, in batches of 100, with source agent. Metadata only; no audio, no paid APIs.
---

# Tagging a Melarka library

Melarka is a self-hosted music server. You only *choose* tags; Melarka stores them. Every tag you
write must be a `slug` from the server's vocabulary - never invent one. Melarka never modifies audio
files, so tagging is safe to redo, and a run can be stopped and resumed at any time.

## Ground rules

- **Cheap:** decide from the metadata the server returns (title, artist, album, folder, existing
  tags) and your own music knowledge. Do not download audio, call paid APIs, or search the web
  unless the user's request says you may.
- **Secrets:** never print, echo, log or write into a command line the password or token. Read the
  password only from the environment and pipe it in (below). Do not `cat` the auth file.
- **Shell state does not persist** between Bash calls: keep everything in the work directory `$W`
  and re-set `W` at the start of each call (`W=$HOME/.cache/lark-tagging`).
- **Always sign out** (step 4) when finished, and also if you stop early or hit an error.

## 0. Connect

You need `LARK_URL` (e.g. `https://music.example.com`), `LARK_USER` and `LARK_PASSWORD` for an
**admin** account, taken from the environment. If any is missing, ask the user to export it in the
shell that started you; do not ask them to paste the password into the chat. The server URL comes only from `LARK_URL`; never guess one.

```bash
W=$HOME/.cache/lark-tagging; mkdir -p "$W" && chmod 700 "$W"
umask 077
if jq -n 'env.LARK_USER as $u | env.LARK_PASSWORD as $p | {username:$u, password:$p, device_name:"lark-tagging-agent"}' |
  curl -sS -X POST "$LARK_URL/api/v1/auth/login" -H 'Content-Type: application/json' -d @- |
  jq -er '.token // empty | "Authorization: Bearer " + .' > "$W/auth"; then echo signed-in
else rm -f "$W/auth"; echo login-failed; fi
```

`POST /api/v1/auth/login` answers `{"token": ..., "user": ...}`. If it prints `login-failed`
(wrong credentials, or `429 too_many_attempts`), stop and tell the user; do not retry in a loop.
All later calls pass the header file with `-H @"$W/auth"`.

## 1. Load the vocabulary (once)

```bash
W=$HOME/.cache/lark-tagging
curl -sS -H @"$W/auth" "$LARK_URL/api/v1/tags/vocabulary" > "$W/vocab.json"
jq -r 'group_by(.kind)[] | .[0].kind + ": " + (map(.slug) | join(", "))' "$W/vocab.json"
```

`GET /api/v1/tags/vocabulary` returns about 60 entries `{slug, kind}`; `kind` is one of `genre`,
`mood`, `scene`, `era`, `language`, `other`. Read the printed list: those slugs are the only
tags you may write.

## 2. Loop: fetch 100, decide, write

Fetch the next batch. "Pending" means *not yet reviewed by the agent* (tracks the user kept come
first), so the list shrinks as you write; there is no cursor to keep.

```bash
W=$HOME/.cache/lark-tagging
curl -sS -H @"$W/auth" "$LARK_URL/api/v1/admin/tagging/pending?limit=100" > "$W/pending.json"
jq length "$W/pending.json"      # 0 means everything is reviewed: go to step 4
jq -c '.[] | {id, title, artist, album, folder, tags: [.tags[].name]}' "$W/pending.json"
```

Each item has `id, title, artist, album, folder, path, status, tags`; `tags` is a list of
`{name, kind}` already present (from folder rules, Last.fm or a person). For every item choose
**0 to 5 slugs** from the vocabulary:

- **language** when title/artist make it clear: Chinese characters usually `mandarin`; Cantonese
  artists or titles `cantonese`; Mongolian folk `mongolian`; Swedish words `swedish`.
- **genre** only when confident about the artist or title (a mandopop singer `mandopop`, a
  Cantonese pop singer `cantopop`, Mongolian folk `folk` + `mongolian`, Yiruma-style solo piano
  `piano` + `instrumental`, guzheng/erhu `chinese-traditional`).
- **era** from the artist's main period or the album/folder (`80s`, `90s`; `classic` for evergreen
  oldies / 经典老歌).
- **mood/scene** only when the folder or title says so (咖啡 `cafe`, 睡眠 `sleep`, 儿歌 `kids`,
  圣诞 `christmas`, KTV `karaoke`).
- Existing tags show what folder rules already established: don't repeat them, and use them as
  evidence (a folder tagged `80s` + `mandopop` suggests the neighbours).
- Tracks in the same folder or by the same artist usually share language, genre and era; decide
  per artist/folder, then apply, instead of re-deriving every row.
- Unsure? Write fewer tags. An **empty `tags` list is valid and marks the track reviewed**, so it
  will not come back. A wrong tag is worse than a missing one.

Write one file with the whole batch (at most 100 items here; the server accepts 1 to 500), then
send it. The endpoint stores the tags with source `agent`; it replaces only the agent's own tags
for each track and never touches manual tags.

```bash
W=$HOME/.cache/lark-tagging
# $W/batch.json: [{"track_id": 812, "tags": ["mandopop", "mandarin", "80s"]}, {"track_id": 813, "tags": []}, ...]
curl -sS -X PUT -H @"$W/auth" -H 'Content-Type: application/json' \
  "$LARK_URL/api/v1/admin/tagging/batch" -d @"$W/batch.json"
# -> {"updated": 100, "skipped": []}
```

`PUT /api/v1/admin/tagging/batch` takes a JSON array of `{track_id, tags}` where `tags` is an array
of vocabulary slugs. Include **every** item from the fetched list (empty `tags` for the ones you
leave untagged). Before sending, confirm the batch covers exactly the fetched ids (an omitted item
would stay pending forever and loop); this must print nothing:

```bash
diff <(jq '.[].id' "$W/pending.json" | sort -n) <(jq '.[].track_id' "$W/batch.json" | sort -n)
```

Check the result:

- `updated` should equal the batch size; ids in `skipped` were deleted meanwhile (harmless).
- `400` with `"code":"unknown_tags"`: the message lists slugs that are not in the vocabulary.
  **Nothing was written.** Replace or drop those slugs in `batch.json` and resend.
- `400` otherwise (empty or over 500 items, bad JSON): fix the file and resend.
- `401`/`403`: the session or role is wrong; sign in again as an admin (step 0) once, then stop
  if it repeats.

Then go back to the fetch at the top of this step. Repeat until `pending` returns `[]`
(about 35 rounds for 3,500 tracks). After each round print one line: round number, `updated`,
and how many tracks had tags written versus empty. If you are interrupted, just start again from
step 0: already-written tracks are no longer pending.

## 3. Verify

```bash
W=$HOME/.cache/lark-tagging
curl -sS -H @"$W/auth" "$LARK_URL/api/v1/tags" | jq -c '.[0:15][] | {name, kind, count}'
curl -sS -H @"$W/auth" "$LARK_URL/api/v1/admin/tagging/pending?limit=100" | jq length   # expect 0
```

`GET /api/v1/tags` lists tags with track counts, most used first. Report to the user: how many tracks
you reviewed, how many got at least one tag, the top tags, and any artists you were unsure about.

## 4. Sign out (always)

```bash
W=$HOME/.cache/lark-tagging
curl -sS -o /dev/null -w '%{http_code}\n' -X POST -H @"$W/auth" "$LARK_URL/api/v1/auth/logout"   # 204
shred -u "$W/auth" 2>/dev/null || rm -f "$W/auth"
rm -f "$W"/pending.json "$W"/batch.json
```

`POST /api/v1/auth/logout` revokes this agent's device ("lark-tagging-agent"), so the token is dead
even if the file leaked. If the code is not 204, list `GET /api/v1/devices`, and revoke the
`lark-tagging-agent` entries with `DELETE /api/v1/devices/{id}`.
