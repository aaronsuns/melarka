package download

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aaronsuns/lark-server/internal/fileutil"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Adopt turns a file already on disk (a kept preview) into a finished
// download of v requested by userID — no yt-dlp. The job is created
// straight in "downloading" (no worker can claim it), the file is moved to
// the path a download of v would use, scanned, linked, auto-favorited and
// placed in lists exactly like a download. When a job already covers v
// (queued, running, or done with a usable track) nothing moves: userID is
// recorded as a requester of that job (favorited when done) and it is
// returned with adopted=false; the caller deletes its own copy. When the
// moved file cannot be ingested it is moved back to src.
func (s *Service) Adopt(ctx context.Context, userID int64, v ytdlp.Video, src string) (Job, bool, error) {
	if !ytdlp.IsVideoID(v.ID) {
		return Job{}, false, ytdlp.ErrBadURL
	}
	lib, err := s.target(ctx)
	if err != nil {
		return Job{}, false, err
	}
	v.URL, v.Thumbnail = ytdlp.WatchURL(v.ID), ytdlp.ThumbnailURL(v.ID)
	now := s.now().Unix()
	r, err := s.DB.ExecContext(ctx, `INSERT INTO downloads(user_id,url,video_id,title,channel,duration_s,thumbnail,status,created_at,updated_at)
		SELECT ?,?,?,?,?,?,?,'downloading',?,? WHERE NOT EXISTS (SELECT 1 FROM downloads d WHERE `+dupWhere+`)`,
		userID, v.URL, v.ID, v.Title, v.Channel, v.DurationS, v.Thumbnail, now, now, v.ID)
	if err != nil {
		return Job{}, false, err
	}
	if n, err := r.RowsAffected(); err != nil {
		return Job{}, false, err
	} else if n == 0 {
		j, err := s.insertOrExisting(ctx, userID, v)
		return j, false, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return Job{}, false, err
	}
	j, err := s.get(ctx, id)
	if err != nil {
		return Job{}, false, err
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO download_requests(download_id,user_id,created_at) VALUES (?,?,?)`, id, userID, now); err != nil {
		return Job{}, false, err
	}
	destNoExt, title, artist := destFor(lib, j)
	dst := destNoExt + filepath.Ext(src)
	if err := fileutil.Move(src, dst); err != nil {
		s.finish(ctx, id, StatusFailed, "lark:not_ingested")
		return Job{}, false, fmt.Errorf("download: adopt %s: %w", v.ID, err)
	}
	trackID, fail := s.ingest(ctx, id, lib, dst, title, artist)
	if fail != "" {
		// Give the file back, so the caller still has it (a preview can
		// then still be played or kept elsewhere); never leave it behind.
		if err := fileutil.Move(dst, src); err != nil {
			s.Log.Warn("download: adopt: give the file back", "job", id, "err", err)
			os.Remove(dst)
		}
		s.finish(ctx, id, StatusFailed, fail)
		return Job{}, false, fmt.Errorf("download: adopt %s: %s", v.ID, fail)
	}
	if err := s.finishDone(ctx, id, trackID, dst); err != nil {
		return Job{}, true, err
	}
	if err := s.favoriteRequesters(ctx, id, trackID); err != nil {
		s.Log.Warn("download: auto-favorite", "job", id, "err", err)
	}
	if err := s.placeInLists(ctx, v.ID, trackID); err != nil {
		s.Log.Warn("download: place in playlists", "job", id, "err", err)
	}
	j, err = s.get(ctx, id)
	return j, true, err
}
