package personal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type Queue struct {
	TrackIDs     []int64 `json:"track_ids"`
	CurrentIndex int     `json:"current_index"`
	PositionMS   int64   `json:"position_ms"`
	Version      int64   `json:"version"`
	UpdatedBy    string  `json:"updated_by"`
	UpdatedAt    int64   `json:"updated_at"`
}

func (s *Store) Queue(ctx context.Context, userID int64) (Queue, error) {
	var q Queue
	var ids string
	err := s.DB.QueryRowContext(ctx, `SELECT track_ids,current_index,position_ms,version,updated_by,updated_at FROM play_queue WHERE user_id=?`, userID).
		Scan(&ids, &q.CurrentIndex, &q.PositionMS, &q.Version, &q.UpdatedBy, &q.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Queue{TrackIDs: []int64{}}, nil
	}
	if err != nil {
		return q, err
	}
	err = json.Unmarshal([]byte(ids), &q.TrackIDs)
	return q, err
}

func (s *Store) SaveQueue(ctx context.Context, userID int64, q Queue, by string) (Queue, error) {
	if q.TrackIDs == nil {
		q.TrackIDs = []int64{}
	}
	if q.CurrentIndex < 0 || (len(q.TrackIDs) > 0 && q.CurrentIndex >= len(q.TrackIDs)) || q.PositionMS < 0 {
		return Queue{}, fmt.Errorf("%w: current_index/position out of range", ErrInvalid)
	}
	ids, _ := json.Marshal(q.TrackIDs)
	now := s.now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO play_queue(user_id,track_ids,current_index,position_ms,version,updated_by,updated_at)
		VALUES (?,?,?,?,1,?,?) ON CONFLICT(user_id) DO UPDATE SET track_ids=excluded.track_ids, current_index=excluded.current_index,
		position_ms=excluded.position_ms, version=play_queue.version+1, updated_by=excluded.updated_by, updated_at=excluded.updated_at`,
		userID, string(ids), q.CurrentIndex, q.PositionMS, by, now)
	if err != nil {
		return Queue{}, err
	}
	return s.Queue(ctx, userID)
}
