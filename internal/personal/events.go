package personal

import (
	"context"
	"database/sql"
	"errors"
)

// KeepAfterPlays qualifying plays move a pending track to kept.
const KeepAfterPlays = 3

type PlayEvent struct {
	ClientEventID string `json:"client_event_id"`
	TrackID       int64  `json:"track_id"`
	StartedAt     int64  `json:"started_at"`
	PlayedSeconds int64  `json:"played_seconds"`
	Skipped       bool   `json:"skipped"`
	Quality       string `json:"quality"`
}

// RecordPlays stores events idempotently by (user, client_event_id): the app
// replays its offline queue until it sees a response, so duplicates are normal.
func (s *Store) RecordPlays(ctx context.Context, userID, deviceID int64, evs []PlayEvent) (int, error) {
	var dev any
	if deviceID > 0 {
		dev = deviceID
	}
	accepted := 0
	for _, e := range evs {
		if e.ClientEventID == "" {
			continue
		}
		var durMS int64
		err := s.DB.QueryRowContext(ctx, `SELECT duration_ms FROM tracks WHERE id=?`, e.TrackID).Scan(&durMS)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return accepted, err
		}
		started := e.StartedAt
		if started == 0 {
			started = s.now()
		}
		ok, err := s.recordOne(ctx, userID, dev, e, started, durMS)
		if err != nil {
			return accepted, err
		}
		if ok {
			accepted++
		}
	}
	return accepted, nil
}

// recordOne inserts one play event and, if it qualifies, bumps keep_plays in
// the same transaction. Both statements commit or roll back together: if the
// keep_plays update failed after a bare insert had already committed, a
// later replay of the same client_event_id would be seen as a duplicate (by
// the UNIQUE(user_id, client_event_id) constraint) and the lost increment
// could never be recovered.
func (s *Store) recordOne(ctx context.Context, userID int64, dev any, e PlayEvent, started, durMS int64) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO play_events(user_id,track_id,device_id,client_event_id,started_at,played_seconds,skipped,quality)
		VALUES (?,?,?,?,?,?,?,?)`, userID, e.TrackID, dev, e.ClientEventID, started, e.PlayedSeconds, e.Skipped, e.Quality)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil // replay
	}
	if e.PlayedSeconds >= 240 || e.PlayedSeconds*1000*2 >= durMS {
		if _, err := tx.ExecContext(ctx, `UPDATE tracks SET keep_plays=keep_plays+1,
			status=CASE WHEN keep_plays+1 >= ? THEN 'kept' ELSE status END
			WHERE id=? AND status='pending'`, KeepAfterPlays, e.TrackID); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
