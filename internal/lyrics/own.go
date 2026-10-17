package lyrics

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

// maxOwnLyrics caps lyrics a person saves (runes).
const maxOwnLyrics = 20000

// ErrBadLyrics: lyrics a person saved that are empty or too long.
var ErrBadLyrics = errors.New("lyrics: empty or too long")

// SetOwn stores lyrics a person made for the track (pasted text, timed in the
// app: LRC when timed, else plain) and selects them for everyone, like an
// admin's pick. They are one candidate per wording (source "user"): timing
// the same words again updates it, and a person's words are never "rejected"
// (an earlier report of the same words is lifted). Returns what Get now
// answers.
func (s *Service) SetOwn(ctx context.Context, trackID int64, text string) (Result, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" || utf8.RuneCountInString(text) > maxOwnLyrics {
		return Result{}, ErrBadLyrics
	}
	if _, err := BuildQuery(ctx, s.DB, s.Library, trackID); err != nil {
		return Result{}, err
	}
	_, synced := ParseLRC(text)
	hash := textHash(text)
	now := s.now().Unix()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO lyrics(track_id,source,external_id,synced,text,selected,created_at)
		VALUES (?,'user',?,?,?,0,?)
		ON CONFLICT(track_id,source,external_id) DO UPDATE SET synced=excluded.synced, text=excluded.text
		RETURNING id`, trackID, "user:"+hash[:16], synced, text, now).Scan(&id); err != nil {
		return Result{}, err
	}
	if err := selectRow(ctx, tx, trackID, id); err != nil {
		return Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM lyrics_rejected WHERE track_id=? AND text_hash=?`, trackID, hash); err != nil {
		return Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO lyrics_lookup(track_id,attempted_at,found,manual) VALUES (?,?,1,1)
		ON CONFLICT(track_id) DO UPDATE SET attempted_at=excluded.attempted_at, found=1, manual=1, wrong_at=NULL`, trackID, now); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, err
	}
	s.failed.Clear(trackID)
	r, _, err := s.selected(ctx, trackID)
	return r, err
}
