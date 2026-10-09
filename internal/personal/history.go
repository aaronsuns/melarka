package personal

import (
	"context"
	"strings"
	"unicode/utf8"
)

const SearchHistoryCap = 20

const maxSearchRunes = 100

// NormQuery trims and collapses whitespace: the form history is stored and deduped in.
func NormQuery(q string) string { return strings.Join(strings.Fields(q), " ") }

// SearchHistory returns the user's recent searches, newest first, never nil.
func (s *Store) SearchHistory(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT query FROM search_history WHERE user_id=? ORDER BY seq DESC LIMIT ?`, userID, SearchHistoryCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// RecordSearch moves q (after NormQuery; Unicode case-insensitive dedupe, the newest
// spelling kept) to the top and keeps the newest SearchHistoryCap. ErrInvalid when
// empty or longer than 100 runes.
func (s *Store) RecordSearch(ctx context.Context, userID int64, q string) error {
	q = NormQuery(q)
	if q == "" || utf8.RuneCountInString(q) > maxSearchRunes {
		return ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Write first: under WAL a read-then-write transaction can fail with
	// SQLITE_BUSY_SNAPSHOT (see lyrics.Service.store).
	if _, err := tx.ExecContext(ctx, `INSERT INTO search_history(user_id,query,norm_key,seq,searched_at)
		VALUES (?,?,?,(SELECT COALESCE(MAX(seq),0)+1 FROM search_history WHERE user_id=?),?)
		ON CONFLICT(user_id,norm_key) DO UPDATE SET query=excluded.query, seq=excluded.seq, searched_at=excluded.searched_at`,
		userID, q, strings.ToLower(q), userID, s.now()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM search_history WHERE user_id=? AND seq <=
		(SELECT seq FROM search_history WHERE user_id=? ORDER BY seq DESC LIMIT 1 OFFSET ?)`, userID, userID, SearchHistoryCap); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ClearSearchHistory(ctx context.Context, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM search_history WHERE user_id=?`, userID)
	return err
}
