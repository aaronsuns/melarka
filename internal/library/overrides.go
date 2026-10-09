package library

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Overrides holds admin-supplied metadata that takes precedence over tags
// read from the file. nil means "leave unchanged"; a non-nil pointer to ""
// (or, for Year, to 0) clears the override back to the tagged/derived value.
// NoAlbum / NoYear set an explicit "none" instead (stored as album ” and
// year 0): the track shows no album / no year whatever its tags or folder
// say, e.g. for a YouTube channel folder or an upload date. A caller must not
// combine NoAlbum with Album or NoYear with Year.
type Overrides struct {
	Title, Artist, Album *string
	Year                 *int64
	NoAlbum, NoYear      bool
}

func nullStr(p *string) any {
	if p == nil || strings.TrimSpace(*p) == "" {
		return nil
	}
	return strings.TrimSpace(*p)
}

// Editable returns ErrNotFound if trackID doesn't exist or is currently
// trashed. While trashed, a track's rel_path points into ".lark-trash/…"
// (see trash.Service.Move), so admin edits that would trigger Reindex must be
// refused — otherwise the trash path would be re-added to the search index.
func (s *Store) Editable(ctx context.Context, trackID int64) error {
	var status string
	err := s.DB.QueryRowContext(ctx, `SELECT status FROM tracks WHERE id=?`, trackID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status == "trashed" {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetOverrides(ctx context.Context, trackID int64, o Overrides) error {
	if err := s.Editable(ctx, trackID); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO track_overrides(track_id) VALUES (?)`, trackID); err != nil {
		return err
	}
	var oldTitle, oldArtist sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT title, artist FROM track_overrides WHERE track_id=?`, trackID).Scan(&oldTitle, &oldArtist); err != nil {
		return err
	}
	set := func(col string, v any) error {
		_, err := s.DB.ExecContext(ctx, `UPDATE track_overrides SET `+col+`=? WHERE track_id=?`, v, trackID)
		return err
	}
	if o.Title != nil {
		if err := set("title", nullStr(o.Title)); err != nil {
			return err
		}
	}
	if o.Artist != nil {
		if err := set("artist", nullStr(o.Artist)); err != nil {
			return err
		}
	}
	if o.Album != nil {
		if err := set("album", nullStr(o.Album)); err != nil {
			return err
		}
	}
	if o.NoAlbum {
		if err := set("album", ""); err != nil {
			return err
		}
	}
	if o.NoYear {
		if err := set("year", 0); err != nil {
			return err
		}
	}
	if o.Year != nil {
		var v any
		if *o.Year > 0 {
			v = *o.Year
		}
		if err := set("year", v); err != nil {
			return err
		}
	}
	renamed := (o.Title != nil && nullStr(o.Title) != nullable(oldTitle)) || (o.Artist != nil && nullStr(o.Artist) != nullable(oldArtist))
	if renamed {
		if err := s.clearLookupMisses(ctx, trackID); err != nil {
			return err
		}
	}
	if err := s.Reindex(ctx, trackID); err != nil {
		return err
	}
	return s.pruneOrphans(ctx)
}

// nullable is a stored override as nullStr would have written it.
func nullable(v sql.NullString) any {
	if !v.Valid || strings.TrimSpace(v.String) == "" {
		return nil
	}
	return strings.TrimSpace(v.String)
}

// clearLookupMisses forgets the track's lyrics and artwork misses after its
// title or artist changed, so the next lookups search with the new names. A
// manual choice and a "wrong lyrics" report (wrong_at) are kept, and found
// rows and stored lyrics are never touched.
func (s *Store) clearLookupMisses(ctx context.Context, trackID int64) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM lyrics_lookup WHERE track_id=? AND found=0 AND manual=0 AND wrong_at IS NULL`, trackID); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM artwork_lookup WHERE track_id=? AND found=0`, trackID)
	return err
}

// SetStatus moves a track between "kept" and "pending". Trashing goes through
// trash.Service instead, so a caller asking for "trashed" here (or any other
// value) gets a plain validation error, and a track that is already trashed
// is left alone (WHERE status!='trashed' matches zero rows, so this reports
// ErrNotFound rather than silently reviving it).
func (s *Store) SetStatus(ctx context.Context, trackID int64, status string) error {
	if status != "kept" && status != "pending" {
		return errors.New("status must be kept or pending")
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE tracks SET status=? WHERE id=? AND status!='trashed'`, status, trackID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
