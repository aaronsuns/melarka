package library

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// Garbled tags (spec §15.7). Old Chinese MP3s often carry GBK bytes in tags
// flagged as Latin-1, so ffprobe hands us "ÄÇÓ¢" for 那英; others were
// already decoded with the bytes lost ("���"), or say only "Track 7". The
// first kind is repaired, the rest are dropped so the display falls back to
// the file name ("陈星 - 离家的孩子.mp3"). Audio files are never written.

// repairTag decodes Latin-1-range GBK mojibake back to Chinese, and returns s
// unchanged when it is anything else (plain ASCII, real accented Latin text,
// text that already has CJK in it, or bytes that don't decode cleanly).
//
// Mojibake must contain at least one Latin-1 symbol (U+0080–U+00BF, ×, ÷):
// GBK bytes land there constantly ("ÄÇÓ¢", "ÌØ±ð"), while real names made of
// accented letters only ("Sääksjärvi", "Sjöö", "Åå Ää") or CP1251 Cyrillic
// mojibake ("Àíçè") decode as GBK too but never contain one.
func repairTag(s string) string {
	b := make([]byte, 0, len(s))
	symbol := false
	for _, r := range s {
		if r > 0xFF {
			return s // outside Latin-1: already real Unicode text (CJK included)
		}
		if (r >= 0x80 && r <= 0xBF) || r == 0xD7 || r == 0xF7 {
			symbol = true
		}
		b = append(b, byte(r))
	}
	if !symbol || !gbkShaped(b) {
		return s
	}
	out, err := simplifiedchinese.GBK.NewDecoder().Bytes(b)
	if err != nil || !utf8.Valid(out) {
		return s
	}
	han := false
	for _, r := range string(out) {
		switch {
		case r == utf8.RuneError:
			return s
		case unicode.Is(unicode.Han, r):
			han = true
		case !plausibleInChinese(r):
			return s
		}
	}
	if !han {
		return s
	}
	return string(out)
}

// gbkShaped reports whether the high bytes of b pair up like GBK text: every
// high byte is a lead followed by a trail, and at least half of the pairs
// have a high trail byte. Real Latin-1 text ("Björk", "Mötley Crüe") puts a
// single accented letter before an ASCII letter — a valid GBK trail byte, but
// such pairs alone never make up Chinese mojibake.
func gbkShaped(b []byte) bool {
	highTrail, asciiTrail := 0, 0
	for i := 0; i < len(b); i++ {
		if b[i] < 0x80 {
			continue
		}
		if i+1 >= len(b) {
			return false
		}
		if b[i+1] >= 0x80 {
			highTrail++
		} else {
			asciiTrail++
		}
		i++
	}
	return highTrail > 0 && asciiTrail <= highTrail
}

// plausibleInChinese allows the non-Han runes that GBK-encoded titles carry:
// ASCII, the middle dot, CJK and general punctuation, full-width forms, kana.
func plausibleInChinese(r rune) bool {
	switch {
	case r < 0x80, r == 0xB7:
		return true
	case r >= 0x2010 && r <= 0x206F, r >= 0x3000 && r <= 0x30FF, r >= 0xFF00 && r <= 0xFFEF:
		return true
	}
	return false
}

var placeholderTag = regexp.MustCompile(`(?i)^(?:(?:track|音轨|曲目)\s*\d+|unknown|unknown artist|未知|未知艺术家|<unknown>)$`)

// unusableTag reports tags that carry no information: undecodable bytes
// (U+FFFD), only question marks, or a generic placeholder like "Track 7".
func unusableTag(s string) bool {
	if strings.ContainsRune(s, utf8.RuneError) {
		return true
	}
	if strings.TrimFunc(s, func(r rune) bool { return r == '?' || unicode.IsSpace(r) }) == "" {
		return true
	}
	return placeholderTag.MatchString(strings.TrimSpace(s))
}

// cleanTag is the stored form of a file tag: repaired, or "" if unusable.
func cleanTag(s string) string {
	s = repairTag(s)
	if unusableTag(s) {
		return ""
	}
	return s
}

// splitFileName reads "[NN - ]Artist - Title.ext" (or "伊能静-流浪的小孩.ext":
// a single unspaced '-' after a short artist that has CJK in it, so
// "Re-Born", "Spider-Man" and "Jay-Z" stay whole) into its parts; artist is
// "" when the name has none. "01 - 甜蜜蜜" is a track number, not an artist.
func splitFileName(rel string) (artist, title string) {
	base := path.Base(rel)
	base = strings.TrimSuffix(base, path.Ext(base))
	rest := leadingTrackNo.ReplaceAllString(base, "")
	if rest == "" {
		return "", base
	}
	a, t, ok := strings.Cut(rest, " - ")
	if !ok && strings.Count(rest, "-") == 1 {
		a, t, _ = strings.Cut(rest, "-")
		a = strings.TrimSpace(a)
		ok = utf8.RuneCountInString(a) <= 20 && hasHan(a)
	}
	a, t = strings.TrimSpace(a), strings.TrimSpace(t)
	if !ok || a == "" || t == "" || allDigits(a) {
		return "", rest
	}
	return a, t
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// artistFromPath is the artist of "经典怀旧(1)/陈星 - 离家的孩子.mp3" (陈星),
// the display fallback when the artist tag is empty.
func artistFromPath(rel string) string {
	a, _ := splitFileName(rel)
	return a
}

// garbledName reports an artist name that cleanTag would change.
func garbledName(name string) bool { return name != "" && cleanTag(name) != name }

// tagFixKey marks in app_state that RepairStoredTags ran with these rules;
// bump the version when cleanTag changes so stored tags are cleaned again.
const tagFixKey = "tagfix:v1"

type storedTags struct{ title, artist, album, albumArtist string }

func (t storedTags) cleaned() storedTags {
	return storedTags{cleanTag(t.title), cleanTag(t.artist), cleanTag(t.album), cleanTag(t.albumArtist)}
}

// RepairStoredTags applies cleanTag to the tags already stored for every
// non-trashed track, once per tagFixKey: repaired tracks get their stored
// tags rewritten, their artist/album links and search entry rebuilt, and
// their agent-tagging mark cleared so the agent looks at them again (and the
// Last.fm mark too when a real artist name replaced the stored one), their
// lyrics/cover misses forgotten and their albums' artist link rebuilt.
// Tracks a cut-short run left linked to a garbled artist are picked up again. In the
// same pass, tracks indexed before the file-name artist fallback existed —
// no artist tag or override, no artist link, an "Artist - Title" file name —
// are reindexed so they get one. Manual overrides are untouched and still
// win. Returns the number of tracks repaired or relinked. Each track is its
// own short write, so the startup scan running alongside is never blocked
// for long.
func (s *Store) RepairStoredTags(ctx context.Context) (int, error) {
	var cur string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key=?`, tagFixKey).Scan(&cur)
	if err == nil {
		return 0, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	// One read pass picks the candidates; each is re-read before it is
	// written, since the scan may have moved, trashed or re-tagged it since.
	// A track whose tags are already clean but whose artist (or album
	// artist) link is still garbled was left half done by an interrupted
	// run: it is reindexed again.
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id, t.rel_path, t.tag_title, t.tag_artist, t.tag_album, t.tag_album_artist,
		t.artist_id IS NULL AND COALESCE(o.artist,'')='', COALESCE(ar.name,''), COALESCE(aa.name,'')
		FROM tracks t LEFT JOIN track_overrides o ON o.track_id=t.id
		LEFT JOIN artists ar ON ar.id=t.artist_id LEFT JOIN albums al ON al.id=t.album_id LEFT JOIN artists aa ON aa.id=al.artist_id
		WHERE t.status!='trashed' ORDER BY t.id`)
	if err != nil {
		return 0, err
	}
	var ids, stranded, relink []int64
	for rows.Next() {
		var id int64
		var rel, artistName, albumArtistName string
		var t storedTags
		var noArtist bool
		if err := rows.Scan(&id, &rel, &t.title, &t.artist, &t.album, &t.albumArtist, &noArtist, &artistName, &albumArtistName); err != nil {
			rows.Close()
			return 0, err
		}
		switch {
		case t.cleaned() != t:
			ids = append(ids, id)
		case garbledName(artistName) || garbledName(albumArtistName):
			stranded = append(stranded, id)
		case noArtist && t.artist == "" && artistFromPath(rel) != "":
			relink = append(relink, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	// Each track is finished — tags repaired, then artist, album artist and
	// search entry rebuilt — before the next is touched, so an interrupted
	// run leaves at most one track half done, and the selection above finds
	// it again.
	n := 0
	for _, id := range ids {
		ok, err := s.repairTrackTags(ctx, id)
		if err != nil {
			return n, err
		}
		if !ok {
			continue
		}
		if err := s.reindex(ctx, id, true); err != nil {
			return n, err
		}
		n++
	}
	for _, id := range stranded {
		var live bool
		err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM tracks WHERE id=? AND status!='trashed'`, id).Scan(&live)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return n, err
		}
		if err := s.reindex(ctx, id, true); err != nil {
			return n, err
		}
		n++
	}
	for _, id := range relink {
		// Re-read: skip a track trashed, linked or given an artist since.
		var stale bool
		err := s.DB.QueryRowContext(ctx, `SELECT t.artist_id IS NULL AND t.tag_artist='' AND COALESCE(o.artist,'')=''
			FROM tracks t LEFT JOIN track_overrides o ON o.track_id=t.id WHERE t.id=? AND t.status!='trashed'`, id).Scan(&stale)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !stale) {
			continue
		}
		if err != nil {
			return n, err
		}
		if err := s.reindex(ctx, id, true); err != nil {
			return n, err
		}
		n++
	}
	if n > 0 {
		if err := s.pruneOrphans(ctx); err != nil {
			return n, err
		}
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO app_state(key,value) VALUES (?,'done')
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, tagFixKey)
	return n, err
}

// repairTrackTags rewrites one track's stored tags with their cleaned form,
// clears its agent mark (and its Last.fm mark when a real artist name
// replaced the stored one) and drops its remembered lyrics/cover misses, in
// one short transaction. It reports false when
// there is nothing to do: the track is gone, trashed, already clean, or its
// tags changed between the read and the write (the scan stores cleaned tags
// itself).
func (s *Store) repairTrackTags(ctx context.Context, id int64) (bool, error) {
	var t storedTags
	err := s.DB.QueryRowContext(ctx, `SELECT tag_title, tag_artist, tag_album, tag_album_artist FROM tracks WHERE id=? AND status!='trashed'`, id).
		Scan(&t.title, &t.artist, &t.album, &t.albumArtist)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	c := t.cleaned()
	if c == t {
		return false, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `UPDATE tracks SET tag_title=?, tag_artist=?, tag_album=?, tag_album_artist=?
		WHERE id=? AND status!='trashed' AND tag_title=? AND tag_artist=? AND tag_album=? AND tag_album_artist=?`,
		c.title, c.artist, c.album, c.albumArtist, id, t.title, t.artist, t.album, t.albumArtist)
	if err != nil {
		return false, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tagging_state SET agent_at=NULL WHERE track_id=?`, id); err != nil {
		return false, err
	}
	// Misses were searched with the garbled tags: look again. Hits and an
	// admin's lyrics pick stay.
	if _, err := tx.ExecContext(ctx, `DELETE FROM lyrics_lookup WHERE track_id=? AND found=0 AND manual=0`, id); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM artwork_lookup WHERE track_id=? AND found=0`, id); err != nil {
		return false, err
	}
	if c.artist != "" && c.artist != t.artist {
		if _, err := tx.ExecContext(ctx, `UPDATE tagging_state SET lastfm_at=NULL WHERE track_id=?`, id); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}
