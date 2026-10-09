---
name: lark-lyrics
description: Use when the user asks to find missing lyrics in their Melarka (lark) music library (or "fix the lyrics", "歌词找不到", "find lyrics for my favorites") - reads tracks without lyrics from the Melarka (lark) server, works out the real song title and singer(s) from messy YouTube titles, asks Lark to look the lyrics up with them, fixes clearly wrong display names, and tags clear instrumentals. In batches of up to 50, favorites first. Metadata only; no audio, no web, no paid APIs.
---

# Finding missing lyrics in Lark

Lark is a self-hosted music server. It looks lyrics up itself (lrclib, NetEase, QQ, Kugou),
but only finds them when it searches with the real song title and singer. Many YouTube downloads
carry titles like `盧冠廷 莫文蔚【一生所愛 Love In A Life Time】電影「大话西游」插曲【MV】「…」原创remix`,
which Lark could not untangle. Your job is to **recognise the song** and hand Lark the right
title and singer; Lark does the searching, matching and storing. Lark never modifies audio files.

Cleaning up the names themselves (title and singer swapped, junk words, the channel as album, the
upload date as year) is the separate skill `skills/lark-names/SKILL.md` (lark-names, "fix song
names"); run it first when the user asks for both, since its fixed names are what lookups search with.

## Ground rules

- **Cheap:** decide from the metadata the server returns (title, artist, album, folder, YouTube
  title and channel) and your own music knowledge. Do not download audio, search the web, or call
  paid APIs unless the user's request says you may.
- **Confident or skip:** if you cannot tell which song it is, skip it. A wrong title fetches the
  wrong lyrics and makes the display name worse; a skipped track is simply still missing.
- **Manual edits win:** never overwrite a title/artist a person typed (`title_edited` /
  `artist_edited`, see step 2c), and never remove a tag.
- **Secrets:** never print, echo, log or write into a command line the password or token. Read the
  password only from the environment and pipe it in (below). Do not `cat` the auth file.
- **Shell state does not persist** between Bash calls: keep everything in the work directory `$W`
  and re-set `W` at the start of each call (`W=$HOME/.cache/lark-lyrics`).
- **One request at a time.** A lyrics lookup takes up to 6 seconds; never run them in parallel.
- **Always sign out** (step 4) when finished, and also if you stop early or hit an error.

## 0. Connect

You need `LARK_URL` (e.g. `https://music.example.com`, or `http://127.0.0.1:4600` when you
run on the server itself), `LARK_USER` and `LARK_PASSWORD` for an **admin** account, taken from
the environment. If any is missing, ask the user to export it in the shell that started you; do not
ask them to paste the password into the chat. The server URL comes only from `LARK_URL`; never guess one.

```bash
W=$HOME/.cache/lark-lyrics; mkdir -p "$W" && chmod 700 "$W"
umask 077
if jq -n 'env.LARK_USER as $u | env.LARK_PASSWORD as $p | {username:$u, password:$p, device_name:"lark-lyrics-agent"}' |
  curl -sS -X POST "$LARK_URL/api/v1/auth/login" -H 'Content-Type: application/json' -d @- |
  jq -er '.token // empty | "Authorization: Bearer " + .' > "$W/auth"; then echo signed-in
else rm -f "$W/auth"; echo login-failed; fi
: > "$W/results.tsv"; rm -f "$W"/after.*
```

`POST /api/v1/auth/login` answers `{"token": ..., "user": ...}`. If it prints `login-failed`
(wrong credentials, or `429 too_many_attempts`), stop and tell the user; do not retry in a loop.
All later calls pass the header file with `-H @"$W/auth"`.

Note how much there is to do (`GET /api/v1/admin/lyrics/missing/count?scope=`):

```bash
W=$HOME/.cache/lark-lyrics
for S in favorites downloads all; do
  printf '%s ' "$S"; curl -sS -H @"$W/auth" "$LARK_URL/api/v1/admin/lyrics/missing/count?scope=$S" | jq -r .count
done
printf 'reported '; curl -sS -H @"$W/auth" "$LARK_URL/api/v1/admin/lyrics/missing/count?scope=all&reported=1" | jq -r .count
```

## 1. Fetch a batch (50 at most)

Work through four passes in this order. **First the reported tracks, all of them**
(`scope=all&reported=1`: tracks whose lyrics a listener reported wrong, every group, favorites
first; handle them with 2e), then `scope=favorites` (anyone's favorites), then `scope=downloads`
(YouTube downloads that are not favorites), then `scope=all` (everything, which repeats the
earlier passes: skip ids you already handled). Within a pass, page with `after=` the
last id of the previous page, because tracks you could not fix stay on the list.

```bash
W=$HOME/.cache/lark-lyrics
SCOPE=reported   # then favorites, then downloads, then all
AFTER=$(cat "$W/after.$SCOPE" 2>/dev/null || echo 0)
Q="scope=$SCOPE"; [ "$SCOPE" = reported ] && Q="scope=all&reported=1"
curl -sS -H @"$W/auth" "$LARK_URL/api/v1/admin/lyrics/missing?$Q&limit=50&after=$AFTER" > "$W/page.json"
jq length "$W/page.json"   # 0: this pass is done, go to the next scope (after all: step 3)
jq -c --rawfile done "$W/results.tsv" \
  '($done | split("\n") | map(split("\t")[0])) as $d | .[] | select((.id|tostring) as $i | $d | index($i) | not)
   | {id, title, artist, album, folder, duration_s, youtube_title, youtube_channel, last_lookup_at, title_edited, artist_edited, reported_wrong}' "$W/page.json"
```

`GET /api/v1/admin/lyrics/missing?scope=favorites|downloads|all&limit=50&after=<id>` returns
tracks that are not trashed or missing, have no selected lyrics, and are not tagged
`instrumental`, ordered by id within the pass. Each item: `id, title, artist, album, folder, path,
duration_s` (the current display values), plus `youtube_title` and `youtube_channel` (the raw
video title and channel, only for downloads), `last_lookup_at` / `found` (only if Lark has
looked before), and `title_edited` / `artist_edited`: `true` when a person typed that name (the
server compares the stored name with what every version of Lark's cleaner made of the video;
anything else set on the track counts as typed), and `reported_wrong`: `true` when someone tapped
**歌词不对** (wrong lyrics) and Lark had no other version left (see 2e). After the batch is handled
(step 2), save the cursor:

```bash
W=$HOME/.cache/lark-lyrics; SCOPE=favorites
jq -r '.[-1].id // empty' "$W/page.json" > "$W/after.$SCOPE.new" && [ -s "$W/after.$SCOPE.new" ] &&
  mv "$W/after.$SCOPE.new" "$W/after.$SCOPE" || rm -f "$W/after.$SCOPE.new"
```

## 2. Per track: identify, fix the name, look up, tag

For every item (not already in `results.tsv`), in order:

### 2a. Identify the song

From `title`, `artist`, `youtube_title`, `youtube_channel`, `folder` and `album`, decide the
**real song title** and **singer(s)** as the song is officially released:

- The film/drama/anime the song comes from is context, not the title: in
  `電影「大话西游」插曲` the song is not 大话西游. 插曲/主题曲/片尾曲/片头曲 name the work.
- A song name often sits in `【…】` right after the singers, or in `《…》` / `「…」`; a long
  `「…」` is usually a lyric line, not the title. Drop translations (`Love In A Life Time`), and
  `MV`, `Lyrics`, `动态歌词`, `高音质`, `原创remix`, `纯享`, `cover`.
- The channel (`華音殿Music Channel`, `经典老歌频道`) is almost never the singer; folder names
  are sometimes the singer or the album.
- Duets: list every singer, joined with `, ` (`卢冠廷, 莫文蔚`).
- Keep the script the song is known by (mainland songs simplified, HK/TW songs often
  traditional); Lark's matching folds simplified and traditional, so either works for matching.
- **Instrumental?** Piano/guzheng/erhu/BGM pieces, `纯音乐`, `伴奏`, `Instrumental`, sleep/study
  music with no singer: mark it instrumental (2d) instead of looking up lyrics.
- Not sure which song it is? Record `skipped` (2f) and move on.

### 2b. Is the display name wrong?

The user approved fixing names that are clearly wrong. Judge **each field on its own**, and fix a
field only when it is **not edited** (`title_edited` / `artist_edited` is `false`) **and** wrong:
the work name as title (`大话西游`), brackets or film words in the artist
(`盧冠廷 莫文蔚【一生所愛…】電影`), the channel as artist when the singer is known, or noise words
left in. A field that is already right stays as it is. Send **one field per call**, never both
"to be safe":

```bash
W=$HOME/.cache/lark-lyrics; ID=812
jq -n --arg t "一生所爱" '{title:$t}' |
  curl -sS -o /dev/null -w '%{http_code}\n' -X PATCH -H @"$W/auth" -H 'Content-Type: application/json' \
  "$LARK_URL/api/v1/tracks/$ID" -d @-   # 204
jq -n --arg a "卢冠廷, 莫文蔚" '{artist:$a}' |
  curl -sS -o /dev/null -w '%{http_code}\n' -X PATCH -H @"$W/auth" -H 'Content-Type: application/json' \
  "$LARK_URL/api/v1/tracks/$ID" -d @-   # 204
```

`PATCH /api/v1/tracks/{id}` takes `{"title"?, "artist"?, "album"?, "year"?}` and stores them as
the track's override (the audio file is untouched); omitted fields stay as they are. Never send an
empty string (that clears the override).

### 2c. Never touch a hand-edited name

A field with `title_edited` / `artist_edited` `true` was typed by a person: leave it alone, even if
you would spell it differently. The only exception is a value that is **plainly garbage** -
mojibake (`ä¸€ç”Ÿ`), a URL, or a channel name (`華音殿Music Channel`) - and then the result line
must say so (`fixed EDITED artist (channel name): … -> …`). You may still look up lyrics with
your own title/artist (2d); that does not change the name.

### 2d. Look the lyrics up

```bash
W=$HOME/.cache/lark-lyrics; ID=812
jq -n --arg t "一生所爱" --arg a "卢冠廷, 莫文蔚" '{title:$t, artist:$a}' |
  curl -sS -X POST -H @"$W/auth" -H 'Content-Type: application/json' \
  "$LARK_URL/api/v1/tracks/$ID/lyrics/refresh" -d @- | jq -c '{found, source, synced, instrumental}'
```

`POST /api/v1/tracks/{id}/lyrics/refresh` with `{"title", "artist"}` searches every provider with
exactly those values (within about 6 seconds) and, on a match, stores and selects the lyrics:
`{"found": true, "source": "netease", "synced": true, ...}`. `404` means the track is gone
(record `gone`).

If `found` is `false`, try **at most 2 variants**, one call each, then stop:

- the title in the other script (一生所爱 / 一生所愛), or the singer's other spelling;
- each singer alone (`卢冠廷`, then `莫文蔚`) for a duet;
- the title without `(Live)`, `(Remix)`, `DJ版`, `女声版`, `完整版`.

Do not search with an empty artist (any song with a similar title would match), except in the
broad pass below. If ten lookups in a row come back `found: false`, including songs you are sure
are well known, the lyrics services may be down: stop, sign out, and tell the user. Lookups for
`reported_wrong` tracks don't count toward those ten (their known words are rejected, so
`found: false` is expected there).

**Broad pass (favorites only).** The user wants lyrics for their favorites even if they may be wrong:
they check them while the song plays and pick another candidate or delete the wrong one in
**Change lyrics**. So in the `scope=favorites` pass (and for tracks the user explicitly asked about),
when a track is still `found: false` after the normal attempts and their variants, and you know
the song, ask Lark for a broad match, at most 2 calls: first with your best title and singer, then
with the title only:

```bash
W=$HOME/.cache/lark-lyrics; ID=812
jq -n --arg t "一生所爱" --arg a "卢冠廷, 莫文蔚" '{title:$t, artist:$a, broad:true}' |
  curl -sS -X POST -H @"$W/auth" -H 'Content-Type: application/json' \
  "$LARK_URL/api/v1/tracks/$ID/lyrics/refresh" -d @- | jq -c '{found, source, synced}'
# still found:false → once more, title only:
jq -n --arg t "一生所爱" '{title:$t, artist:"", broad:true}' |
  curl -sS -X POST -H @"$W/auth" -H 'Content-Type: application/json' \
  "$LARK_URL/api/v1/tracks/$ID/lyrics/refresh" -d @- | jq -c '{found, source, synced}'
```

With `"broad": true` Lark accepts any singer, a looser title (one containing the other, or close
spelling) and a duration up to 20 seconds off; it keeps up to 10 candidates, strict matches first,
and selects the best. Accept what it selects (do not pick among candidates yourself) and record
the line with `BROAD` (2f). Broad search is never on scope=all or `scope=downloads` on your own:
only favorites, tracks the user named, and `reported_wrong` tracks (2e). Do not broad-search a song you skipped as unsure: a loose
search on a wrong title is just noise. A track the user marked "This song has no lyrics" is no longer
on the missing list, so you will not see it.

**Instrumental:** add the `instrumental` tag without removing the track's other tags. The tag
endpoint takes the track's **whole** manual tag list (`{"source":"manual","tags":[...]}`; any
visible tag left out would be removed), so read the current tags first and append:

```bash
W=$HOME/.cache/lark-lyrics; ID=812
curl -sS -H @"$W/auth" "$LARK_URL/api/v1/tracks/$ID/tags" > "$W/tags.json"
jq -e 'type == "array"' "$W/tags.json" >/dev/null &&
  jq -c '{source: "manual", tags: (. + [{name: "instrumental", kind: "genre"}] | unique_by(.name))}' "$W/tags.json" |
  curl -sS -o /dev/null -w '%{http_code}\n' -X PUT -H @"$W/auth" -H 'Content-Type: application/json' \
  "$LARK_URL/api/v1/tracks/$ID/tags" -d @-   # 204
```

`GET /api/v1/tracks/{id}/tags` lists the visible tags `[{name, kind}]`; `PUT
/api/v1/tracks/{id}/tags` with source `manual` makes that list the track's tags (manual tags win
over every other source). If the GET did not return an array, do not PUT. Lark skips lyrics
lookups for instrumental tracks, and they leave the missing list.

### 2e. Lyrics reported wrong (`reported_wrong: true`)

Someone listening said the lyrics Lark had were wrong, and every version Lark had stored is now
**rejected for good**: Lark never stores or selects those words again, from any provider, so a
search that only finds them answers `found: false`. The usual cause is the wrong version of the
song (another singer's cover, a live or remix cut, a different arrangement with other timing). So:

- Work out which recording this track is: the `youtube_title`, `duration_s`, folder and album
  often say live (`Live`, `演唱会`, `现场`), remix/DJ, a cover (`翻唱`, another singer than the
  original) or the original. A track a few seconds shorter or longer than the studio release is
  usually another cut.
- Try the lookups that would find **another** version, at most 4 calls in all: the exact
  version first (title with `(Live)` / the cover singer as artist), then the original singer
  (the cover's lyrics are the same words, but the original's timing may fit better), then a broad
  search (`broad:true`, with your best title and singer, then title only) — for reported tracks
  the broad pass is allowed in **every** scope, since a person already asked for a fix.
- Accept what Lark selects; never try to re-select or re-add the rejected text (you cannot, and
  must not work around it, e.g. by pasting lyrics).
- Still `found: false`: record `no-lyrics REPORTED (tried: …)` so the user sees it in the report.

### 2f. Record one line per track

```bash
W=$HOME/.cache/lark-lyrics
printf '%s\t%s\n' 812 'found netease synced; fixed 大话西游 / 盧冠廷 莫文蔚【…】電影 -> 一生所爱 / 卢冠廷, 莫文蔚' >> "$W/results.tsv"
```

Use one of: `found <source>[ synced]` (optionally `; fixed title|artist <old> -> <new>`, with
`EDITED` and the reason when you changed an edited field), `found <source>[ synced] BROAD` for a
broad-pass result (add `title-only` when the second call found it: `found kugou synced BROAD
title-only`), `no-lyrics (tried: …)`,
`instrumental`, `skipped (unsure)`, `gone`. Add `REPORTED` to the line of every
`reported_wrong` track (`found qq synced BROAD REPORTED`). Before saving the cursor and fetching the next
page, confirm every id of the page has a line; this must print nothing:

```bash
W=$HOME/.cache/lark-lyrics
jq -r '.[].id' "$W/page.json" | grep -vxFf <(cut -f1 "$W/results.tsv")
```

After each batch print one line: scope, batch size, found / fixed / instrumental / no-lyrics /
skipped counts. Then save the cursor (end of step 1) and fetch the next page. If interrupted,
start again from step 0: tracks that got lyrics or the instrumental tag are no longer listed.

## 3. Verify and report

```bash
W=$HOME/.cache/lark-lyrics
for S in favorites downloads all; do
  printf '%s ' "$S"; curl -sS -H @"$W/auth" "$LARK_URL/api/v1/admin/lyrics/missing/count?scope=$S" | jq -r .count
done
printf 'reported '; curl -sS -H @"$W/auth" "$LARK_URL/api/v1/admin/lyrics/missing/count?scope=all&reported=1" | jq -r .count
cut -f2 "$W/results.tsv" | cut -d' ' -f1 | sort | uniq -c
grep -c BROAD "$W/results.tsv"
grep -c REPORTED "$W/results.tsv"
```

Report to the user: the counts before and after per scope, how many tracks got lyrics, how many
names you corrected (list them: old -> new), how many you tagged instrumental, and the tracks you
skipped or found no lyrics for (title + YouTube title), so they can fix those by hand in
**Change lyrics**. List the `BROAD` finds separately (title + source): they may be wrong, and the user
checks them while they play (another candidate, delete, or "This song has no lyrics"). List the
`REPORTED` tracks too, with what was found or tried.

## 4. Sign out (always)

```bash
W=$HOME/.cache/lark-lyrics
curl -sS -o /dev/null -w '%{http_code}\n' -X POST -H @"$W/auth" "$LARK_URL/api/v1/auth/logout"   # 204
shred -u "$W/auth" 2>/dev/null || rm -f "$W/auth"
rm -f "$W"/page.json "$W"/tags.json "$W"/after.*
```

`POST /api/v1/auth/logout` revokes this agent's device ("lark-lyrics-agent"), so the token is dead
even if the file leaked. If the code is not 204, list `GET /api/v1/devices`, and revoke the
`lark-lyrics-agent` entries with `DELETE /api/v1/devices/{id}`. Keep `results.tsv` until the user has
read the report.
