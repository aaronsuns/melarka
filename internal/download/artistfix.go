package download

import (
	"context"
	"database/sql"
	"errors"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// artistFixKey marks in app_state that FixArtistNames has run.
const artistFixKey = "dlartist:v1"

// FixArtistNames gives downloads tagged before ytdlp.CleanArtist existed the
// cleaned names, once (per artistFixKey): for each done job with a
// non-trashed track whose override artist (or title) is still exactly what
// ytdlp.CleanTitleV1 made of the video — i.e. nobody edited it — and differs
// from what CleanTitle makes of it now, the override is rewritten, the track
// reindexed, and its lyrics and cover misses forgotten so they are looked up
// with the new name (an admin's lyrics pick and found lyrics/covers stay).
// Each track is its own short write. Returns the number of tracks changed.
func (s *Service) FixArtistNames(ctx context.Context) (int, error) {
	var cur string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key=?`, artistFixKey).Scan(&cur)
	if err == nil {
		return 0, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	type cand struct {
		track               int64
		oldTitle, oldArtist string
		newTitle, newArtist string
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT d.track_id, d.title, d.channel FROM downloads d
		JOIN tracks t ON t.id=d.track_id WHERE d.status='done' AND t.status!='trashed' ORDER BY d.track_id, d.id`)
	if err != nil {
		return 0, err
	}
	var cands []cand
	for rows.Next() {
		var id int64
		var title, channel string
		if err := rows.Scan(&id, &title, &channel); err != nil {
			rows.Close()
			return 0, err
		}
		c := cand{track: id}
		c.oldTitle, c.oldArtist = ytdlp.CleanTitleV1(title, channel)
		c.newTitle, c.newArtist = ytdlp.CleanTitle(title, channel)
		if c.oldTitle != c.newTitle || c.oldArtist != c.newArtist {
			cands = append(cands, c)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	n := 0
	for _, c := range cands {
		ok, err := s.fixTrackNames(ctx, c.track, c.oldTitle, c.oldArtist, c.newTitle, c.newArtist)
		if err != nil {
			return n, err
		}
		if !ok {
			continue
		}
		if err := s.Library.Reindex(ctx, c.track); err != nil {
			return n, err
		}
		n++
	}
	if n > 0 {
		if err := s.Library.PruneOrphans(ctx); err != nil {
			return n, err
		}
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO app_state(key,value) VALUES (?,'done')
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, artistFixKey)
	return n, err
}

// fixTrackNames rewrites one track's override title and artist where each
// still holds its old cleaned value, and forgets the track's lookup misses,
// in one short transaction. It reports false when nothing changed (the track
// is gone or trashed, or both names were edited since).
func (s *Service) fixTrackNames(ctx context.Context, id int64, oldTitle, oldArtist, newTitle, newArtist string) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed := false
	for _, f := range []struct{ col, old, new string }{{"artist", oldArtist, newArtist}, {"title", oldTitle, newTitle}} {
		if f.old == f.new {
			continue
		}
		r, err := tx.ExecContext(ctx, `UPDATE track_overrides SET `+f.col+`=? WHERE track_id=? AND `+f.col+`=?
			AND track_id IN (SELECT id FROM tracks WHERE status!='trashed')`, f.new, id, f.old)
		if err != nil {
			return false, err
		}
		if k, _ := r.RowsAffected(); k > 0 {
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM lyrics_lookup WHERE track_id=? AND found=0 AND manual=0`, id); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM artwork_lookup WHERE track_id=? AND found=0`, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
