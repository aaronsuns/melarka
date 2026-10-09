// Package personal stores per-user data: favorites, dislikes, playlists,
// play events and the synced play queue.
package personal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrNotFound = errors.New("not found")

// ErrInvalid marks a caller-input validation failure (as opposed to a
// storage error): handlers map it to 400, not 500.
var ErrInvalid = errors.New("invalid input")

type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() int64 {
	if s.Now != nil {
		return s.Now().Unix()
	}
	return time.Now().Unix()
}

func (s *Store) trackExists(ctx context.Context, id int64) error {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tracks WHERE id=? AND status!='trashed'`, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// setMark toggles a row in table (with opposite cleared when turning on).
// table and opposite are always one of the two hardcoded literals below
// ("favorites"/"dislikes") — never derived from user input.
func (s *Store) setMark(ctx context.Context, table, opposite string, userID, trackID int64, on bool) error {
	if !on {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id=? AND track_id=?`, userID, trackID)
		return err
	}
	if err := s.trackExists(ctx, trackID); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+opposite+` WHERE user_id=? AND track_id=?`, userID, trackID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO `+table+`(user_id,track_id,created_at) VALUES (?,?,?)`, userID, trackID, s.now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetFavorite(ctx context.Context, userID, trackID int64, on bool) error {
	return s.setMark(ctx, "favorites", "dislikes", userID, trackID, on)
}

func (s *Store) SetDislike(ctx context.Context, userID, trackID int64, on bool) error {
	return s.setMark(ctx, "dislikes", "favorites", userID, trackID, on)
}

type Playlist struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	TrackCount int    `json:"track_count"`
	UpdatedAt  int64  `json:"updated_at"`
}

const playlistSelect = `SELECT p.id, p.name, (SELECT COUNT(*) FROM playlist_items i WHERE i.playlist_id=p.id), p.updated_at FROM playlists p`

func (s *Store) Playlists(ctx context.Context, userID int64) ([]Playlist, error) {
	rows, err := s.DB.QueryContext(ctx, playlistSelect+` WHERE p.user_id=? ORDER BY p.updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Playlist{}
	for rows.Next() {
		var p Playlist
		if err := rows.Scan(&p.ID, &p.Name, &p.TrackCount, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreatePlaylist(ctx context.Context, userID int64, name string, trackIDs []int64) (Playlist, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Playlist{}, fmt.Errorf("%w: name required", ErrInvalid)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Playlist{}, err
	}
	defer tx.Rollback()
	now := s.now()
	res, err := tx.ExecContext(ctx, `INSERT INTO playlists(user_id,name,created_at,updated_at) VALUES (?,?,?,?)`, userID, name, now, now)
	if err != nil {
		return Playlist{}, err
	}
	id, _ := res.LastInsertId()
	if err := writeItems(ctx, tx, id, trackIDs); err != nil {
		return Playlist{}, err
	}
	if err := tx.Commit(); err != nil {
		return Playlist{}, err
	}
	p, _, err := s.Playlist(ctx, userID, id)
	return p, err
}

func writeItems(ctx context.Context, tx *sql.Tx, playlistID int64, ids []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM playlist_items WHERE playlist_id=?`, playlistID); err != nil {
		return err
	}
	for i, tid := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO playlist_items(playlist_id,position,track_id) VALUES (?,?,?)`, playlistID, i, tid); err != nil {
			if strings.Contains(err.Error(), "FOREIGN KEY") {
				return ErrNotFound
			}
			return err
		}
	}
	return nil
}

func (s *Store) Playlist(ctx context.Context, userID, id int64) (Playlist, []int64, error) {
	var p Playlist
	err := s.DB.QueryRowContext(ctx, playlistSelect+` WHERE p.id=? AND p.user_id=?`, id, userID).Scan(&p.ID, &p.Name, &p.TrackCount, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, nil, ErrNotFound
	}
	if err != nil {
		return Playlist{}, nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT track_id FROM playlist_items WHERE playlist_id=? ORDER BY position`, id)
	if err != nil {
		return Playlist{}, nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var tid int64
		rows.Scan(&tid)
		ids = append(ids, tid)
	}
	return p, ids, rows.Err()
}

func (s *Store) UpdatePlaylist(ctx context.Context, userID, id int64, name *string, trackIDs *[]int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE playlists SET updated_at=? WHERE id=? AND user_id=?`, s.now(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if name != nil {
		if strings.TrimSpace(*name) == "" {
			return fmt.Errorf("%w: name required", ErrInvalid)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE playlists SET name=? WHERE id=?`, strings.TrimSpace(*name), id); err != nil {
			return err
		}
	}
	if trackIDs != nil {
		if err := writeItems(ctx, tx, id, *trackIDs); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeletePlaylist(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM playlists WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
