package download

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// ListRef names the Lark playlist a downloaded YouTube list fills.
type ListRef struct {
	PlaylistID int64  `json:"id"`
	Name       string `json:"name"`
	ListID     string `json:"list_id"`
}

// Enqueued is what EnqueueURL queued.
type Enqueued struct {
	Jobs     []Job    `json:"jobs"`
	Playlist *ListRef `json:"playlist,omitempty"` // set when the URL named a whole YouTube list
}

// isWholeList reports whether URL u (already resolved to l) names a whole
// YouTube list: no single video in it (watch?v=…&list=… stays "this song"),
// the list= it carries is the one yt-dlp resolved (so a channel never
// counts), and not a mix.
func isWholeList(u string, l ytdlp.List) bool {
	return l.ID != "" && ytdlp.VideoIDFromURL(u) == "" && ytdlp.ListIDFromURL(u) == l.ID && !ytdlp.IsMixID(l.ID)
}

// recordList records userID's download of list l, in one transaction: the
// download_lists row is upserted (title refreshed); when it has no Lark
// playlist (never made, or the user deleted it), the user's playlist named
// exactly the title that no other list owns is adopted, else one is created;
// the list's items are replaced by the resolved order.
func (s *Service) recordList(ctx context.Context, userID int64, l ytdlp.List) (ListRef, error) {
	title := strings.TrimSpace(l.Title)
	if title == "" {
		title = "YouTube " + l.ID
	}
	now := s.now().Unix()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ListRef{}, err
	}
	defer tx.Rollback()
	// A write first, so the transaction takes SQLite's write lock up front
	// (busy_timeout applies) instead of failing on a read→write upgrade.
	var row int64
	var pid sql.NullInt64
	if err := tx.QueryRowContext(ctx, `INSERT INTO download_lists(user_id,list_id,title,created_at,updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(user_id,list_id) DO UPDATE SET title=excluded.title, updated_at=excluded.updated_at
		RETURNING id, playlist_id`, userID, l.ID, title, now, now).Scan(&row, &pid); err != nil {
		return ListRef{}, err
	}
	ref := ListRef{ListID: l.ID}
	if pid.Valid {
		if err := tx.QueryRowContext(ctx, `SELECT name FROM playlists WHERE id=?`, pid.Int64).Scan(&ref.Name); err != nil {
			return ListRef{}, err
		}
		ref.PlaylistID = pid.Int64
	} else {
		err := tx.QueryRowContext(ctx, `SELECT id FROM playlists p WHERE p.user_id=? AND p.name=?
			AND NOT EXISTS (SELECT 1 FROM download_lists l WHERE l.playlist_id=p.id) ORDER BY p.id LIMIT 1`,
			userID, title).Scan(&ref.PlaylistID)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(ctx, `INSERT INTO playlists(user_id,name,created_at,updated_at) VALUES (?,?,?,?) RETURNING id`,
				userID, title, now, now).Scan(&ref.PlaylistID)
		}
		if err != nil {
			return ListRef{}, err
		}
		ref.Name = title
		if _, err := tx.ExecContext(ctx, `UPDATE download_lists SET playlist_id=? WHERE id=?`, ref.PlaylistID, row); err != nil {
			return ListRef{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM download_list_items WHERE list_row=?`, row); err != nil {
		return ListRef{}, err
	}
	for i, v := range l.Videos {
		if _, err := tx.ExecContext(ctx, `INSERT INTO download_list_items(list_row,position,video_id) VALUES (?,?,?)`, row, i, v.ID); err != nil {
			return ListRef{}, err
		}
	}
	return ref, tx.Commit()
}

// placeInLists puts trackID (the download of videoID) into the Lark playlist
// of every recorded YouTube list containing videoID, at its YouTube place.
// It is idempotent: a playlist already holding the track is left alone.
func (s *Service) placeInLists(ctx context.Context, videoID string, trackID int64) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT l.id, l.playlist_id, MIN(i.position) FROM download_list_items i
		JOIN download_lists l ON l.id=i.list_row WHERE i.video_id=? AND l.playlist_id IS NOT NULL GROUP BY l.id`, videoID)
	if err != nil {
		return err
	}
	type target struct{ row, playlist, pos int64 }
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.row, &t.playlist, &t.pos); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	var errs []error
	for _, t := range targets {
		if err := s.insertOrdered(ctx, t.row, t.playlist, t.pos, trackID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// insertOrdered inserts trackID into playlistID unless it is already there:
// after the last item whose position in list row listRow is smaller than pos,
// else before the first item with a larger one, else at the end. Items with
// no list position (added by hand) keep their relative place. Positions are
// rewritten and the playlist's updated_at bumped in one transaction.
func (s *Service) insertOrdered(ctx context.Context, listRow, playlistID, pos, trackID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Write first (takes the write lock up front); no row means the
	// playlist was deleted meanwhile.
	r, err := tx.ExecContext(ctx, `UPDATE playlists SET updated_at=? WHERE id=?`, s.now().Unix(), playlistID)
	if err != nil {
		return err
	}
	if n, err := r.RowsAffected(); err != nil || n == 0 {
		return err
	}
	// Each item with the list position of the video it was downloaded from
	// (-1 when it isn't from this list).
	rows, err := tx.QueryContext(ctx, `SELECT p.track_id, COALESCE((SELECT MIN(li.position) FROM download_list_items li
			JOIN downloads d ON d.video_id=li.video_id WHERE li.list_row=? AND d.track_id=p.track_id), -1)
		FROM playlist_items p WHERE p.playlist_id=? ORDER BY p.position`, listRow, playlistID)
	if err != nil {
		return err
	}
	var tracks, listPos []int64
	for rows.Next() {
		var tid, lp int64
		if err := rows.Scan(&tid, &lp); err != nil {
			rows.Close()
			return err
		}
		if tid == trackID {
			rows.Close()
			return nil // already there (rollback undoes the updated_at bump)
		}
		tracks, listPos = append(tracks, tid), append(listPos, lp)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	at := -1
	for i, lp := range listPos {
		if lp >= 0 && lp < pos {
			at = i + 1
		}
	}
	if at < 0 {
		at = len(tracks)
		for i, lp := range listPos {
			if lp > pos {
				at = i
				break
			}
		}
	}
	tracks = slices.Insert(tracks, at, trackID)
	if _, err := tx.ExecContext(ctx, `DELETE FROM playlist_items WHERE playlist_id=?`, playlistID); err != nil {
		return err
	}
	for i, tid := range tracks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO playlist_items(playlist_id,position,track_id) VALUES (?,?,?)`, playlistID, i, tid); err != nil {
			return err
		}
	}
	return tx.Commit()
}
